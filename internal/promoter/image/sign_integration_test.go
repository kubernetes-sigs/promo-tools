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
	"net/http/httptest"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	ociremote "github.com/sigstore/cosign/v2/pkg/oci/remote"
	"github.com/stretchr/testify/require"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	reg "sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// newTLSTestRegistry starts an in-process OCI registry over TLS and returns
// the host:port address and a DefaultPromoterImplementation configured with
// a transport that trusts the test server's certificate.
func newTLSTestRegistry(t *testing.T) (string, *DefaultPromoterImplementation) {
	t.Helper()

	s := httptest.NewTLSServer(registry.New())
	t.Cleanup(s.Close)

	host := s.Listener.Addr().String()

	// Create a rate-limited transport wrapping the TLS-aware client transport.
	rt := ratelimit.NewRoundTripperWithBase(ratelimit.MaxEvents, s.Client().Transport)

	di := &DefaultPromoterImplementation{
		transport: rt,
	}

	return host, di
}

// pushTestImage creates a random image and pushes it to the given reference
// using the TLS-aware transport. Returns the image digest.
func pushTestImage(t *testing.T, di *DefaultPromoterImplementation, ref string) string {
	t.Helper()

	img, err := random.Image(1024, 1)
	require.NoError(t, err)

	r, err := name.ParseReference(ref)
	require.NoError(t, err)

	err = remote.Write(r, img, remote.WithTransport(di.getTransport()))
	require.NoError(t, err)

	d, err := img.Digest()
	require.NoError(t, err)

	return d.String()
}

// testEdgeForHost constructs a promotion edge suitable for local registry tests.
func testEdgeForHost(host string, digest image.Digest) promotion.Edge {
	return promotion.Edge{
		SrcRegistry: reg.Context{Name: image.Registry(host + "/staging"), Src: true},
		SrcImageTag: promotion.ImageTag{Name: "myimage", Tag: "v1.0"},
		Digest:      digest,
		DstRegistry: reg.Context{Name: image.Registry(host + "/production")},
		DstImageTag: promotion.ImageTag{Name: "myimage", Tag: "v1.0"},
	}
}

// --- copyAttachedObjects tests ---

func TestCopyAttachedObjectsSignatureExists(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	// Push a "real" image to staging.
	imgRef := host + "/staging/myimage:v1.0"
	digest := pushTestImage(t, di, imgRef)

	// Push a fake signature artifact to the staging registry using the
	// cosign tag convention: sha256-<hash>.sig
	sigTag := digestToSignatureTag(image.Digest(digest))
	sigRef := fmt.Sprintf("%s/staging/myimage:%s", host, sigTag)
	pushTestImage(t, di, sigRef)

	edge := testEdgeForHost(host, image.Digest(digest))

	err := di.copyAttachedObjects(&edge)
	require.NoError(t, err)

	// Verify the signature landed in the production registry.
	dstSigRef := fmt.Sprintf("%s/production/myimage:%s", host, sigTag)
	ref, err := name.ParseReference(dstSigRef)
	require.NoError(t, err)

	_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
	require.NoError(t, err, "signature should exist in production")
}

func TestCopyAttachedObjectsSignatureMissing(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	// Push a "real" image but no signature.
	imgRef := host + "/staging/myimage:v1.0"
	digest := pushTestImage(t, di, imgRef)

	edge := testEdgeForHost(host, image.Digest(digest))

	// Should gracefully succeed when no signature exists (404 is not an error).
	err := di.copyAttachedObjects(&edge)
	require.NoError(t, err)
}

// --- pushAttestation tests ---

// fakeGenerator is a provenance.Generator that returns a fixed attestation.
type fakeGenerator struct {
	data []byte
}

func (f *fakeGenerator) Generate(_ context.Context, _ *provenance.PromotionRecord) ([]byte, error) {
	return f.data, nil
}

func TestPushAttestation(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	imgRef := host + "/production/myimage:v1.0"
	digest := pushTestImage(t, di, imgRef)

	edge := testEdgeForHost(host, image.Digest(digest))
	record := &provenance.PromotionRecord{
		SrcRef:    edge.SrcReference(),
		DstRef:    edge.DstReference(),
		Digest:    string(edge.Digest),
		BuilderId: "https://k8s.io/promo-tools@test",
	}

	gen := &fakeGenerator{data: []byte(`{"test": "attestation"}`)}

	err := di.pushAttestation(context.Background(), &edge, gen, record)
	require.NoError(t, err)

	// Verify the attestation landed with the correct predicate type.
	digestRef, err := name.NewDigest(fmt.Sprintf("%s/production/myimage@%s", host, digest))
	require.NoError(t, err)

	remoteOpt := ociremote.WithRemoteOptions(remote.WithTransport(di.getTransport()))
	require.True(t, hasPredicateType(digestRef, provenance.PredicateType, remoteOpt),
		"attestation with predicate type should exist")
}

func TestPushAttestationIdempotent(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	imgRef := host + "/production/myimage:v1.0"
	digest := pushTestImage(t, di, imgRef)

	edge := testEdgeForHost(host, image.Digest(digest))
	record := &provenance.PromotionRecord{
		SrcRef:    edge.SrcReference(),
		DstRef:    edge.DstReference(),
		Digest:    string(edge.Digest),
		BuilderId: "https://k8s.io/promo-tools@test",
	}

	gen := &fakeGenerator{data: []byte(`{"test": "attestation"}`)}

	// First push should succeed.
	err := di.pushAttestation(context.Background(), &edge, gen, record)
	require.NoError(t, err)

	// Second push should skip because predicate type already exists.
	err = di.pushAttestation(context.Background(), &edge, gen, record)
	require.NoError(t, err)

	// Verify exactly one attestation layer exists (not duplicated).
	digestRef, err := name.NewDigest(fmt.Sprintf("%s/production/myimage@%s", host, digest))
	require.NoError(t, err)

	remoteOpt := ociremote.WithRemoteOptions(remote.WithTransport(di.getTransport()))
	se := ociremote.SignedUnknown(digestRef, remoteOpt)

	atts, err := se.Attestations()
	require.NoError(t, err)

	sigs, err := atts.Get()
	require.NoError(t, err)
	require.Len(t, sigs, 1, "should have exactly one attestation layer")
}

// --- Integration test for the full promotion flow with CraneProvider ---

func TestPromoteImagesCraneProvider(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	// Set up the CraneProvider to use the TLS-aware transport.
	di.SetRegistryProvider(reg.NewCraneProvider(
		reg.WithTransport(di.getTransport()),
	))

	srcRegistry := image.Registry(host + "/staging")
	dstRegistry := image.Registry(host + "/production")

	type entry struct {
		imgName image.Name
		tag     image.Tag
		digest  string
	}

	entries := []entry{
		{imgName: "app", tag: "v1.0"},
		{imgName: "app", tag: "v2.0"},
		{imgName: "web", tag: "latest"},
	}

	// Push test images to staging.
	for i, e := range entries {
		ref := fmt.Sprintf("%s/%s:%s", srcRegistry, e.imgName, e.tag)
		entries[i].digest = pushTestImage(t, di, ref)
	}

	// Build promotion edges.
	edges := make(map[promotion.Edge]any)

	for _, e := range entries {
		edge := promotion.Edge{
			SrcRegistry: reg.Context{Name: srcRegistry, Src: true},
			SrcImageTag: promotion.ImageTag{Name: e.imgName, Tag: e.tag},
			Digest:      image.Digest(e.digest),
			DstRegistry: reg.Context{Name: dstRegistry},
			DstImageTag: promotion.ImageTag{Name: e.imgName, Tag: e.tag},
		}
		edges[edge] = nil
	}

	// Run promotion.
	opts := &options.Options{Threads: 4}
	err := di.PromoteImages(context.Background(), opts, edges)
	require.NoError(t, err)

	// Verify all images landed.
	for _, e := range entries {
		dstRef := fmt.Sprintf("%s/%s:%s", dstRegistry, e.imgName, e.tag)
		ref, err := name.ParseReference(dstRef)
		require.NoError(t, err)

		desc, err := remote.Head(ref, remote.WithTransport(di.getTransport()))
		require.NoError(t, err, "image %s should exist", dstRef)
		require.Equal(t, e.digest, desc.Digest.String())
	}

	// Verify tag listing.
	repo, err := name.NewRepository(fmt.Sprintf("%s/app", dstRegistry))
	require.NoError(t, err)

	tags, err := remote.List(repo, remote.WithTransport(di.getTransport()))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"v1.0", "v2.0"}, tags)
}

// TestWriteProvenanceAttestationsIdempotent verifies that running
// WriteProvenanceAttestations twice completes without error, and the
// second run skips already-existing attestations.
func TestWriteProvenanceAttestationsIdempotent(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	srcRegistry := image.Registry(host + "/staging")
	dstRegistry := image.Registry(host + "/production")

	digest := pushTestImage(t, di, fmt.Sprintf("%s/app:v1.0", srcRegistry))

	edges := map[promotion.Edge]any{
		{
			SrcRegistry: reg.Context{Name: srcRegistry, Src: true},
			SrcImageTag: promotion.ImageTag{Name: "app", Tag: "v1.0"},
			Digest:      image.Digest(digest),
			DstRegistry: reg.Context{Name: dstRegistry},
			DstImageTag: promotion.ImageTag{Name: "app", Tag: "v1.0"},
		}: nil,
	}

	opts := &options.Options{
		MaxSignatureOps: 10,
	}

	gen := &fakeGenerator{data: []byte(`{"test": "attestation"}`)}

	// Run twice — both should succeed without error.
	for i := range 2 {
		err := di.WriteProvenanceAttestations(context.Background(), opts, edges, gen)
		require.NoError(t, err, "run %d", i+1)
	}

	// Verify the attestation exists with the correct predicate type.
	digestRef, err := name.NewDigest(fmt.Sprintf("%s/app@%s", dstRegistry, digest))
	require.NoError(t, err)

	remoteOpt := ociremote.WithRemoteOptions(remote.WithTransport(di.getTransport()))
	require.True(t, hasPredicateType(digestRef, provenance.PredicateType, remoteOpt),
		"attestation should exist in production")
}
