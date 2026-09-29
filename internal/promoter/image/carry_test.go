/*
Copyright 2026 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package imagepromoter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	"github.com/stretchr/testify/require"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// carryTestPredicateType is the predicate type of the staging attestations
// the tests carry.
const carryTestPredicateType = "https://slsa.dev/provenance/v1"

// carryTestOptions are the options carrying runs with.
func carryTestOptions() *options.Options {
	return &options.Options{SignImages: true, MaxSignatureOps: 10}
}

// stagingAttestation attaches a signed attestation to the staging image of
// the edge and returns it as discovery reports it.
func stagingAttestation(
	t *testing.T, di *DefaultPromoterImplementation, edge *promotion.Edge, subject string,
) *provenance.Attestation {
	t.Helper()

	repo := string(edge.SrcRegistry.Name) + "/" + string(edge.SrcImageTag.Name)

	digest, err := name.NewDigest(repo + "@" + string(edge.Digest))
	require.NoError(t, err)

	before, err := di.bundleReferrers(digest, carryTestPredicateType)
	require.NoError(t, err)

	statement := fmt.Appendf(nil,
		`{"_type": "https://in-toto.io/Statement/v1", "subject": [{"name": %q, "digest": {"sha256": %q}}], `+
			`"predicateType": %q, "predicate": {}}`,
		subject, string(edge.Digest)[len("sha256:"):], carryTestPredicateType,
	)

	bundleJSON, err := (&certBundleSigner{identity: testSignerIdentity, t: t}).SignStatement(statement)
	require.NoError(t, err)
	require.NoError(t, ociremote.WriteAttestationNewBundleFormat(digest, bundleJSON, carryTestPredicateType,
		ociremote.WithRemoteOptions(di.remoteOptions()...)))

	after, err := di.bundleReferrers(digest, carryTestPredicateType)
	require.NoError(t, err)
	require.Len(t, after, len(before)+1)

	for _, ref := range after {
		if !containsDigest(before, ref) {
			return referrerAttestation(t, di, edge, ref)
		}
	}

	require.FailNow(t, "no new referrer")

	return nil
}

// containsDigest reports whether the digests hold the digest.
func containsDigest(digests []name.Digest, digest name.Digest) bool {
	for _, d := range digests {
		if d.DigestStr() == digest.DigestStr() {
			return true
		}
	}

	return false
}

// referrerAttestation returns the attestation in a referrer as discovery
// reports it.
func referrerAttestation(
	t *testing.T, di *DefaultPromoterImplementation, edge *promotion.Edge, ref name.Digest,
) *provenance.Attestation {
	t.Helper()

	img, err := remote.Image(ref, di.remoteOptions()...)
	require.NoError(t, err)

	mf, err := img.Manifest()
	require.NoError(t, err)
	require.Len(t, mf.Layers, 1)

	return &provenance.Attestation{
		Digest:         string(edge.Digest),
		Source:         provenance.SourceReferrer,
		Location:       ref.DigestStr(),
		Layer:          mf.Layers[0].Digest.String(),
		PredicateType:  carryTestPredicateType,
		Status:         provenance.SignatureVerified,
		SubjectMatches: true,
	}
}

// satisfied returns the outcome of one satisfied policy accepting the
// attestations.
func satisfied(accepted ...*provenance.Attestation) *provenance.ImageProvenance {
	return &provenance.ImageProvenance{
		Results: []*provenance.PolicyResult{{Satisfied: true, Accepted: accepted}},
	}
}

// promotedTestEdge pushes an image to staging, copies it to production
// and returns the edge promoting it.
func promotedTestEdge(t *testing.T, di *DefaultPromoterImplementation, host string) promotion.Edge {
	t.Helper()

	digest := pushTestImage(t, di, host+"/staging/myimage:"+testTagV1)
	edge := testEdgeForHost(host, image.Digest(digest))

	require.NoError(t, craneCopyWithTimeout(context.Background(),
		host+"/staging/myimage@"+digest, host+"/production/myimage:"+testTagV1,
		di.craneOptions()))

	return edge
}

// productionReferrers returns the attestation referrers of the promoted
// image of the edge.
func productionReferrers(t *testing.T, di *DefaultPromoterImplementation, edge *promotion.Edge) []name.Digest {
	t.Helper()

	digest, err := name.NewDigest(edge.DstReference())
	require.NoError(t, err)

	refs, err := di.bundleReferrers(digest, carryTestPredicateType)
	require.NoError(t, err)

	return refs
}

func TestCarryAttestations(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)
	edge := promotedTestEdge(t, di, host)
	accepted := stagingAttestation(t, di, &edge, "registry.k8s.io/myimage")

	// Another region of the same image gets no copy of its own.
	other := edge
	other.DstRegistry.Name = image.Registry(host + "/production-eu")

	edges := map[promotion.Edge]any{edge: nil, other: nil}
	results := map[string]*provenance.ImageProvenance{edge.SrcReference(): satisfied(accepted)}

	require.Empty(t, productionReferrers(t, di, &edge))
	require.NoError(t, di.CarryAttestations(context.Background(), carryTestOptions(), edges, results))

	refs := productionReferrers(t, di, &edge)
	require.Len(t, refs, 1)
	require.Equal(t, accepted.Location, refs[0].DigestStr(), "the attestation is copied digest-identical")

	// Carrying again changes nothing.
	require.NoError(t, di.CarryAttestations(context.Background(), carryTestOptions(), edges, results))
	require.Len(t, productionReferrers(t, di, &edge), 1)
}

func TestCarryAttestationsNotCarried(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)
	edge := promotedTestEdge(t, di, host)
	att := stagingAttestation(t, di, &edge, "registry.k8s.io/myimage")
	edges := map[promotion.Edge]any{edge: nil}

	for name, results := range map[string]map[string]*provenance.ImageProvenance{
		"no policy": {},
		"policy not satisfied": {edge.SrcReference(): {Results: []*provenance.PolicyResult{
			{Accepted: []*provenance.Attestation{att}},
		}}},
	} {
		require.NoError(t, di.CarryAttestations(context.Background(), carryTestOptions(), edges, results), name)
	}

	// Like the promotion records, nothing is carried without signing.
	require.NoError(t, di.CarryAttestations(context.Background(), &options.Options{MaxSignatureOps: 10},
		edges, map[string]*provenance.ImageProvenance{edge.SrcReference(): satisfied(att)}))

	// A referrer whose layer is not the accepted bundle is not carried.
	other := *att
	other.Layer = testDigest
	require.NoError(t, di.CarryAttestations(context.Background(), carryTestOptions(),
		edges, map[string]*provenance.ImageProvenance{edge.SrcReference(): satisfied(&other)}))

	require.Empty(t, productionReferrers(t, di, &edge))
}

func TestCarryAttestationsSources(t *testing.T) {
	t.Parallel()

	// The same digest promoted from two staging repositories carries what
	// each accepted, once, and one failure does not stop the others.
	host, di := newTLSTestRegistry(t)
	edge := promotedTestEdge(t, di, host)
	accepted := stagingAttestation(t, di, &edge, "registry.k8s.io/myimage")

	other := edge
	other.SrcRegistry.Name = image.Registry(host + "/staging-other")
	require.NoError(t, craneCopyWithTimeout(context.Background(),
		host+"/staging/myimage@"+string(edge.Digest), host+"/staging-other/myimage@"+string(edge.Digest),
		di.craneOptions()))

	otherAccepted := stagingAttestation(t, di, &other, "registry.k8s.io/myimage-other")
	missing := *otherAccepted
	missing.Location = testDigest

	results := map[string]*provenance.ImageProvenance{
		edge.SrcReference():  satisfied(accepted),
		other.SrcReference(): satisfied(&missing, otherAccepted, accepted),
	}

	err := di.CarryAttestations(context.Background(), carryTestOptions(),
		map[promotion.Edge]any{edge: nil, other: nil}, results)
	require.ErrorContains(t, err, testDigest)
	require.Len(t, productionReferrers(t, di, &edge), 2)
}

func TestCarryAttestationsReferrersTag(t *testing.T) {
	t.Parallel()

	// Without the referrers API, the referrers of a digest are listed in a
	// tag that every copy updates, so none of the copies may get lost.
	s := httptest.NewTLSServer(registry.New())
	t.Cleanup(s.Close)

	host := s.Listener.Addr().String()
	di := &DefaultPromoterImplementation{
		transport: ratelimit.NewRoundTripperWithBase(ratelimit.MaxEvents, s.Client().Transport),
	}

	edge := promotedTestEdge(t, di, host)

	accepted := make([]*provenance.Attestation, 0, 6)
	for i := range 6 {
		accepted = append(accepted, stagingAttestation(t, di, &edge, fmt.Sprintf("registry.k8s.io/myimage-%d", i)))
	}

	results := map[string]*provenance.ImageProvenance{edge.SrcReference(): satisfied(accepted...)}
	require.NoError(t, di.CarryAttestations(context.Background(), carryTestOptions(),
		map[promotion.Edge]any{edge: nil}, results))
	require.Len(t, productionReferrers(t, di, &edge), 6)
}

func TestCarriedAttestations(t *testing.T) {
	t.Parallel()

	digest := testDigest
	referrer := func(location, layer string) *provenance.Attestation {
		return &provenance.Attestation{Digest: digest, Source: provenance.SourceReferrer, Location: location, Layer: layer}
	}

	a := referrer("sha256:a", "sha256:la")
	b := referrer("sha256:b", "sha256:lb")
	child := &provenance.Attestation{
		Digest: "sha256:child", Source: provenance.SourceReferrer, Location: "sha256:c", Layer: "sha256:lc",
	}
	tag := &provenance.Attestation{Digest: digest, Source: provenance.SourceAttestationTag, Location: "sha256-x.att"}
	record := referrer("sha256:r", "sha256:lr")
	record.PredicateType = provenance.PredicateType
	summary := referrer("sha256:s", "sha256:ls")
	summary.PredicateType = "https://slsa.dev/verification_summary/v1"

	require.Nil(t, carriedAttestations(nil, digest))
	require.Nil(t, carriedAttestations(&provenance.ImageProvenance{}, digest))

	// Only referrers of the digest itself, once each, and none of the kinds
	// the promoter writes itself.
	require.Equal(t, []*provenance.Attestation{a, b},
		carriedAttestations(satisfied(a, child, tag, record, summary, a, b), digest))

	// Every policy has to be satisfied and accept an attestation.
	both := &provenance.ImageProvenance{Results: []*provenance.PolicyResult{
		{Satisfied: true, Accepted: []*provenance.Attestation{a, b}},
		{Satisfied: true, Accepted: []*provenance.Attestation{referrer("sha256:b", "sha256:lb")}},
	}}
	require.Equal(t, []*provenance.Attestation{b}, carriedAttestations(both, digest))

	both.Results[1].Satisfied = false
	require.Nil(t, carriedAttestations(both, digest))
}

func TestCheckBundleReferrer(t *testing.T) {
	t.Parallel()

	const layer = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

	hash := func(digest string) v1.Hash {
		h, err := v1.NewHash(digest)
		require.NoError(t, err)

		return h
	}

	bundleManifest := func(mutate func(*v1.Manifest)) *remote.Descriptor {
		mf := v1.Manifest{
			SchemaVersion: 2,
			MediaType:     types.OCIManifestSchema1,
			ArtifactType:  provenance.BundleArtifactType,
			Config: v1.Descriptor{
				MediaType: "application/vnd.oci.empty.v1+json", Digest: hash(emptyConfigDigest), Size: 2,
			},
			Layers:      []v1.Descriptor{{MediaType: provenance.BundleArtifactType, Digest: hash(layer)}},
			Subject:     &v1.Descriptor{MediaType: types.OCIManifestSchema1, Digest: hash(testDigest)},
			Annotations: map[string]string{bundlePredicateTypeAnnotation: carryTestPredicateType},
		}
		if mutate != nil {
			mutate(&mf)
		}

		data, err := json.Marshal(mf)
		require.NoError(t, err)

		desc := &remote.Descriptor{Manifest: data}
		desc.MediaType = mf.MediaType

		return desc
	}

	att := &provenance.Attestation{Layer: layer, PredicateType: carryTestPredicateType}

	require.NoError(t, checkBundleReferrer(bundleManifest(nil), testDigest, att))

	for name, mutate := range map[string]func(*v1.Manifest){
		"index":         func(m *v1.Manifest) { m.MediaType = types.OCIImageIndex },
		"no subject":    func(m *v1.Manifest) { m.Subject = nil },
		"other subject": func(m *v1.Manifest) { m.Subject.Digest = hash(layer) },
		"config":        func(m *v1.Manifest) { m.Config.Digest = hash(layer) },
		"other layer":   func(m *v1.Manifest) { m.Layers[0].Digest = hash(testDigest) },
		"extra layer":   func(m *v1.Manifest) { m.Layers = append(m.Layers, m.Layers[0]) },
		"layer type":    func(m *v1.Manifest) { m.Layers[0].MediaType = "application/octet-stream" },
		"artifact type": func(m *v1.Manifest) { m.ArtifactType = "application/spdx+json" },
		"annotation": func(m *v1.Manifest) {
			m.Annotations = map[string]string{bundlePredicateTypeAnnotation: provenance.PredicateType}
		},
	} {
		require.Error(t, checkBundleReferrer(bundleManifest(mutate), testDigest, att), name)
	}
}
