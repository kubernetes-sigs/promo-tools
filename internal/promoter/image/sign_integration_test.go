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
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
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
func testEdgeForHost(host, dstPath string, digest image.Digest) promotion.Edge {
	return promotion.Edge{
		SrcRegistry: reg.Context{Name: image.Registry(host + "/staging"), Src: true},
		SrcImageTag: promotion.ImageTag{Name: "myimage", Tag: "v1.0"},
		Digest:      digest,
		DstRegistry: reg.Context{Name: image.Registry(host + "/" + dstPath)},
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

	edge := testEdgeForHost(host, "production", image.Digest(digest))

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

	edge := testEdgeForHost(host, "production", image.Digest(digest))

	// Should gracefully succeed when no signature exists (404 is not an error).
	err := di.copyAttachedObjects(&edge)
	require.NoError(t, err)
}

// --- copySBOM tests ---

func TestCopySBOMExists(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	imgRef := host + "/staging/myimage:v1.0"
	digest := pushTestImage(t, di, imgRef)

	// Push a fake SBOM to staging.
	sbomTag := digestToSBOMTag(image.Digest(digest))
	sbomRef := fmt.Sprintf("%s/staging/myimage:%s", host, sbomTag)
	pushTestImage(t, di, sbomRef)

	edge := testEdgeForHost(host, "production", image.Digest(digest))

	err := di.copySBOM(&edge)
	require.NoError(t, err)

	// Verify the SBOM landed in production.
	dstSBOMRef := fmt.Sprintf("%s/production/myimage:%s", host, sbomTag)
	ref, err := name.ParseReference(dstSBOMRef)
	require.NoError(t, err)

	_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
	require.NoError(t, err, "SBOM should exist in production")
}

func TestCopySBOMMissing(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	imgRef := host + "/staging/myimage:v1.0"
	digest := pushTestImage(t, di, imgRef)

	edge := testEdgeForHost(host, "production", image.Digest(digest))

	// Should gracefully succeed when no SBOM exists.
	err := di.copySBOM(&edge)
	require.NoError(t, err)
}

// --- replicateSignatures tests ---

func TestReplicateSignaturesExists(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	imgRef := host + "/primary/myimage:v1.0"
	digest := pushTestImage(t, di, imgRef)

	// Push a signature to the primary destination.
	sigTag := digestToSignatureTag(image.Digest(digest))
	sigRef := fmt.Sprintf("%s/primary/myimage:%s", host, sigTag)
	pushTestImage(t, di, sigRef)

	// Source edge (primary destination where signature already exists).
	srcEdge := testEdgeForHost(host, "primary", image.Digest(digest))

	// Destination edges (mirrors).
	dstEdge := testEdgeForHost(host, "mirror", image.Digest(digest))

	err := di.replicateSignatures(&srcEdge, []promotion.Edge{dstEdge})
	require.NoError(t, err)

	// Verify the signature was replicated to the mirror.
	mirrorSigRef := fmt.Sprintf("%s/mirror/myimage:%s", host, sigTag)
	ref, err := name.ParseReference(mirrorSigRef)
	require.NoError(t, err)

	_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
	require.NoError(t, err, "signature should exist in mirror")
}

func TestReplicateSignaturesMissing(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	// No signature exists at all — replication should gracefully skip.
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	srcEdge := testEdgeForHost(host, "primary", image.Digest(digest))
	dstEdge := testEdgeForHost(host, "mirror", image.Digest(digest))

	err := di.replicateSignatures(&srcEdge, []promotion.Edge{dstEdge})
	require.NoError(t, err)
}

func TestReplicateSignaturesAlreadyExists(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	imgRef := host + "/primary/myimage:v1.0"
	digest := pushTestImage(t, di, imgRef)

	// Push signature to both primary and mirror.
	sigTag := digestToSignatureTag(image.Digest(digest))
	pushTestImage(t, di, fmt.Sprintf("%s/primary/myimage:%s", host, sigTag))
	pushTestImage(t, di, fmt.Sprintf("%s/mirror/myimage:%s", host, sigTag))

	srcEdge := testEdgeForHost(host, "primary", image.Digest(digest))
	dstEdge := testEdgeForHost(host, "mirror", image.Digest(digest))

	// Should succeed without error (signature already present).
	err := di.replicateSignatures(&srcEdge, []promotion.Edge{dstEdge})
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

	edge := testEdgeForHost(host, "production", image.Digest(digest))
	record := &provenance.PromotionRecord{
		SrcRef:    edge.SrcReference(),
		DstRef:    edge.DstReference(),
		Digest:    string(edge.Digest),
		BuilderID: "https://k8s.io/promo-tools@test",
	}

	gen := &fakeGenerator{data: []byte(`{"test": "attestation"}`)}

	err := di.pushAttestation(context.Background(), &edge, gen, record)
	require.NoError(t, err)

	// Verify the attestation landed.
	attTag := digestToAttestationTag(image.Digest(digest))
	attRef := fmt.Sprintf("%s/production/myimage:%s", host, attTag)
	ref, err := name.ParseReference(attRef)
	require.NoError(t, err)

	_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
	require.NoError(t, err, "attestation should exist in production")
}

func TestPushAttestationIdempotent(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	imgRef := host + "/production/myimage:v1.0"
	digest := pushTestImage(t, di, imgRef)

	edge := testEdgeForHost(host, "production", image.Digest(digest))
	record := &provenance.PromotionRecord{
		SrcRef:    edge.SrcReference(),
		DstRef:    edge.DstReference(),
		Digest:    string(edge.Digest),
		BuilderID: "https://k8s.io/promo-tools@test",
	}

	gen := &fakeGenerator{data: []byte(`{"test": "attestation"}`)}

	// Push twice — second push should skip because attestation already exists.
	for i := range 2 {
		err := di.pushAttestation(context.Background(), &edge, gen, record)
		require.NoError(t, err, "push attempt %d", i+1)
	}
}

// --- Tag convention tests ---

func TestDigestToAttestationTag(t *testing.T) {
	t.Parallel()

	tag := digestToAttestationTag("sha256:abc123")
	require.Equal(t, "sha256-abc123.att", tag)
	require.True(t, strings.HasSuffix(tag, attestationTagSuffix))
}

// --- copyWithRetry / headWithRetry are exercised by the above tests
// through replicateSignatures and copyAttachedObjects. ---

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
	err := di.PromoteImages(opts, edges)
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
