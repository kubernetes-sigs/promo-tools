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
	"fmt"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	"github.com/stretchr/testify/require"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	reg "sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// repairTestOptions are the options the repair checks run with.
func repairTestOptions() *options.Options {
	return &options.Options{SignImages: true, VerificationSummaries: true, MaxSignatureOps: 10}
}

// pushSummary attaches a verification summary of the verifier to the
// digest.
func pushSummary(t *testing.T, di *DefaultPromoterImplementation, ref, verifierID string) {
	t.Helper()

	digest, err := name.NewDigest(ref)
	require.NoError(t, err)

	statement := fmt.Appendf(nil,
		`{"_type": "https://in-toto.io/Statement/v1", "subject": [{"name": %q, "digest": {"sha256": %q}}], `+
			`"predicateType": %q, "predicate": {"verifier": {"id": %q}}}`,
		digest.Context().String(), digest.DigestStr()[len("sha256:"):], provenance.SummaryPredicateType, verifierID,
	)

	bundleJSON, err := testBundle(statement)
	require.NoError(t, err)
	require.NoError(t, ociremote.WriteAttestationNewBundleFormat(digest, bundleJSON, provenance.SummaryPredicateType,
		ociremote.WithRemoteOptions(di.remoteOptions()...)))
}

func TestFindAttestationRepairs(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)
	edge := promotedTestEdge(t, di, host)
	accepted := stagingAttestation(t, di, &edge, "registry.k8s.io/myimage")

	// Another tag of the same digest is repaired along with it.
	other := edge
	other.SrcImageTag.Tag, other.DstImageTag.Tag = "v1", "v1"
	edges := map[promotion.Edge]any{edge: nil, other: nil}

	find := func(summaries bool) map[promotion.Edge]any {
		t.Helper()

		opts := repairTestOptions()
		opts.VerificationSummaries = summaries

		repairs, err := di.FindAttestationRepairs(context.Background(), opts, edges)
		require.NoError(t, err)

		return repairs
	}

	// The staging attestation is not carried.
	require.Equal(t, edges, find(false))

	results := map[string]*provenance.ImageProvenance{edge.SrcReference(): satisfied(accepted)}
	require.NoError(t, di.CarryAttestations(context.Background(), carryTestOptions(), edges, results))
	require.Empty(t, find(false))

	// The promoter's verification summary is missing, another verifier's
	// doesn't count.
	require.Equal(t, edges, find(true))

	pushSummary(t, di, edge.DstReference(), "https://example.com/verifier")
	require.Equal(t, edges, find(true))

	pushSummary(t, di, edge.DstReference(), provenance.SummaryVerifierID)
	require.Empty(t, find(true))
}

func TestFindAttestationRepairsNothingStaged(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)
	edge := promotedTestEdge(t, di, host)
	edges := map[promotion.Edge]any{edge: nil}

	// Attestations the promoter writes itself are never carried, so they
	// are not missing.
	statement := fmt.Appendf(nil,
		`{"_type": "https://in-toto.io/Statement/v1", "subject": [{"name": "staging", "digest": {"sha256": %q}}], `+
			`"predicateType": %q, "predicate": {}}`,
		string(edge.Digest)[len("sha256:"):], provenance.PredicateType,
	)

	bundleJSON, err := testBundle(statement)
	require.NoError(t, err)

	src, err := name.NewDigest(edge.SrcReference())
	require.NoError(t, err)
	require.NoError(t, ociremote.WriteAttestationNewBundleFormat(src, bundleJSON, provenance.PredicateType,
		ociremote.WithRemoteOptions(di.remoteOptions()...)))

	opts := repairTestOptions()
	opts.VerificationSummaries = false

	repairs, err := di.FindAttestationRepairs(context.Background(), opts, edges)
	require.NoError(t, err)
	require.Empty(t, repairs)

	// Signing is needed for any repair.
	opts = repairTestOptions()
	opts.SignImages = false

	repairs, err = di.FindAttestationRepairs(context.Background(), opts, edges)
	require.NoError(t, err)
	require.Empty(t, repairs)
}

func TestFindAttestationRepairsSources(t *testing.T) {
	t.Parallel()

	// The same digest promoted from two staging repositories misses the
	// attestation of the second one.
	host, di := newTLSTestRegistry(t)
	edge := promotedTestEdge(t, di, host)
	accepted := stagingAttestation(t, di, &edge, "registry.k8s.io/myimage")

	other := edge
	other.SrcRegistry.Name = image.Registry(host + "/staging-other")
	require.NoError(t, craneCopyWithTimeout(context.Background(),
		host+"/staging/myimage@"+string(edge.Digest), host+"/staging-other/myimage@"+string(edge.Digest),
		di.craneOptions()))

	opts := repairTestOptions()
	opts.VerificationSummaries = false

	require.NoError(t, di.CarryAttestations(context.Background(), carryTestOptions(),
		map[promotion.Edge]any{edge: nil}, map[string]*provenance.ImageProvenance{edge.SrcReference(): satisfied(accepted)}))

	edges := map[promotion.Edge]any{edge: nil, other: nil}

	repairs, err := di.FindAttestationRepairs(context.Background(), opts, edges)
	require.NoError(t, err)
	require.Empty(t, repairs)

	stagingAttestation(t, di, &other, "registry.k8s.io/myimage-other")

	repairs, err = di.FindAttestationRepairs(context.Background(), opts, edges)
	require.NoError(t, err)
	require.Equal(t, edges, repairs)
}

func TestFindAttestationRepairsError(t *testing.T) {
	t.Parallel()

	// A failed check is reported and does not keep the others from being
	// repaired.
	host, di := newTLSTestRegistry(t)
	edge := promotedTestEdge(t, di, host)
	stagingAttestation(t, di, &edge, "registry.k8s.io/myimage")

	broken := edge
	broken.DstImageTag.Name = "Invalid"

	repairs, err := di.FindAttestationRepairs(context.Background(), repairTestOptions(),
		map[promotion.Edge]any{edge: nil, broken: nil})
	require.ErrorContains(t, err, "Invalid")
	require.Equal(t, map[promotion.Edge]any{edge: nil}, repairs)
}

func TestFindAttestationRepairsNotPromoted(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	// Only in staging, for example because the destination lost it.
	digest := pushTestImage(t, di, host+"/staging/myimage:"+testTagV1)
	edges := map[promotion.Edge]any{testEdgeForHost(host, image.Digest(digest)): nil}

	repairs, err := di.FindAttestationRepairs(context.Background(), repairTestOptions(), edges)
	require.NoError(t, err)
	require.Empty(t, repairs)
}

func TestFindAttestationRepairsIndex(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	// An index with two platform manifests, of which the promoter manifest
	// lists only the index and one child.
	idx, err := random.Index(256, 1, 2)
	require.NoError(t, err)

	indexDigest, err := idx.Digest()
	require.NoError(t, err)

	for _, repo := range []string{"/staging/", "/production/"} {
		ref, err := name.NewDigest(host + repo + testImageApp + "@" + indexDigest.String())
		require.NoError(t, err)
		require.NoError(t, remote.WriteIndex(ref, idx, remote.WithTransport(di.getTransport())))
	}

	im, err := idx.IndexManifest()
	require.NoError(t, err)

	listed, unlisted := im.Manifests[0].Digest.String(), im.Manifests[1].Digest.String()

	edge := func(digest string, tag image.Tag) promotion.Edge {
		return promotion.Edge{
			SrcRegistry: reg.Context{Name: image.Registry(host + "/staging"), Src: true},
			SrcImageTag: promotion.ImageTag{Name: testImageApp, Tag: tag},
			Digest:      image.Digest(digest),
			DstRegistry: reg.Context{Name: image.Registry(host + "/production")},
			DstImageTag: promotion.ImageTag{Name: testImageApp, Tag: tag},
		}
	}

	indexEdge, listedEdge := edge(indexDigest.String(), testTagV1), edge(listed, "")
	edges := map[promotion.Edge]any{indexEdge: nil, listedEdge: nil}

	find := func() map[promotion.Edge]any {
		t.Helper()

		repairs, err := di.FindAttestationRepairs(context.Background(), repairTestOptions(), edges)
		require.NoError(t, err)

		return repairs
	}

	dst := host + "/production/" + testImageApp + "@"
	pushSummary(t, di, dst+indexDigest.String(), provenance.SummaryVerifierID)
	pushSummary(t, di, dst+listed, provenance.SummaryVerifierID)

	// The unlisted platform manifest misses its summary, which is written
	// with the index.
	require.Equal(t, map[promotion.Edge]any{indexEdge: nil}, find())

	pushSummary(t, di, dst+unlisted, provenance.SummaryVerifierID)
	require.Empty(t, find())
}
