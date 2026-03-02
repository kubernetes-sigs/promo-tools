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
		BuilderID: "https://k8s.io/promo-tools@test",
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
		BuilderID: "https://k8s.io/promo-tools@test",
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

// --- ReplicateSignatures (batch-listing path) integration tests ---

// prodPath returns a registry path that includes the production repository
// path so that targetIdentity normalizes all mirrors to the same identity
// (registry.k8s.io/<image>). This mirrors real-world registries like
// "us-west2-docker.pkg.dev/k8s-artifacts-prod/images".
func prodPath(prefix string) string {
	return prefix + "/k8s-artifacts-prod/images"
}

// makeProdEdge constructs a promotion edge using production-like registry
// paths so that edges across different registries share the same identity.
func makeProdEdge(host, dstPrefix, imgName string, tag image.Tag, digest image.Digest) promotion.Edge {
	return promotion.Edge{
		SrcRegistry: reg.Context{Name: image.Registry(host + "/" + prodPath("staging")), Src: true},
		SrcImageTag: promotion.ImageTag{Name: image.Name(imgName), Tag: tag},
		Digest:      digest,
		DstRegistry: reg.Context{Name: image.Registry(host + "/" + prodPath(dstPrefix))},
		DstImageTag: promotion.ImageTag{Name: image.Name(imgName), Tag: tag},
	}
}

// TestReplicateSignaturesBatchCopiesMissing verifies that the batch-listing
// ReplicateSignatures method copies signatures that exist on the primary
// registry but are missing from the mirrors.
//
// Registry prefixes are chosen so that "aa-primary" sorts alphabetically
// before "bb-mirror*", matching the groupEdgesByIdentityDigest convention
// where group[0] is the source (alphabetically first).
func TestReplicateSignaturesBatchCopiesMissing(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	primary := prodPath("aa-primary")
	mirror1 := prodPath("bb-mirror1")
	mirror2 := prodPath("bb-mirror2")

	// Push two images to the primary registry.
	d1 := pushTestImage(t, di, host+"/"+primary+"/app:v1.0")
	d2 := pushTestImage(t, di, host+"/"+primary+"/app:v2.0")

	// Push signatures only to the primary.
	sigTag1 := digestToSignatureTag(image.Digest(d1))
	sigTag2 := digestToSignatureTag(image.Digest(d2))

	pushTestImage(t, di, fmt.Sprintf("%s/%s/app:%s", host, primary, sigTag1))
	pushTestImage(t, di, fmt.Sprintf("%s/%s/app:%s", host, primary, sigTag2))

	// Also push the images to the mirrors (but NOT the signatures).
	pushTestImage(t, di, host+"/"+mirror1+"/app:v1.0")
	pushTestImage(t, di, host+"/"+mirror1+"/app:v2.0")
	pushTestImage(t, di, host+"/"+mirror2+"/app:v1.0")
	pushTestImage(t, di, host+"/"+mirror2+"/app:v2.0")

	// Build edges: each image exists in primary + 2 mirrors.
	edges := map[promotion.Edge]any{
		makeProdEdge(host, "aa-primary", "app", "v1.0", image.Digest(d1)): nil,
		makeProdEdge(host, "bb-mirror1", "app", "v1.0", image.Digest(d1)): nil,
		makeProdEdge(host, "bb-mirror2", "app", "v1.0", image.Digest(d1)): nil,
		makeProdEdge(host, "aa-primary", "app", "v2.0", image.Digest(d2)): nil,
		makeProdEdge(host, "bb-mirror1", "app", "v2.0", image.Digest(d2)): nil,
		makeProdEdge(host, "bb-mirror2", "app", "v2.0", image.Digest(d2)): nil,
	}

	opts := &options.Options{
		SignImages:         true,
		MaxSignatureCopies: 10,
	}

	err := di.ReplicateSignatures(opts, edges)
	require.NoError(t, err)

	// Verify signatures landed on both mirrors for both images.
	for _, st := range []string{sigTag1, sigTag2} {
		for _, m := range []string{mirror1, mirror2} {
			refStr := fmt.Sprintf("%s/%s/app:%s", host, m, st)
			ref, err := name.ParseReference(refStr)
			require.NoError(t, err)

			_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
			require.NoError(t, err, "signature %s should exist on %s", st, m)
		}
	}
}

// TestReplicateSignaturesBatchSkipsExisting verifies that the batch-listing
// path correctly skips signatures that already exist on the mirrors.
func TestReplicateSignaturesBatchSkipsExisting(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	primary := prodPath("aa-primary")
	mirror := prodPath("bb-mirror")

	digest := pushTestImage(t, di, host+"/"+primary+"/app:v1.0")
	sigTag := digestToSignatureTag(image.Digest(digest))

	// Push the signature to primary AND mirror (already replicated).
	pushTestImage(t, di, fmt.Sprintf("%s/%s/app:%s", host, primary, sigTag))
	pushTestImage(t, di, fmt.Sprintf("%s/%s/app:%s", host, mirror, sigTag))

	// Also push the images to the mirror.
	pushTestImage(t, di, host+"/"+mirror+"/app:v1.0")

	edges := map[promotion.Edge]any{
		makeProdEdge(host, "aa-primary", "app", "v1.0", image.Digest(digest)): nil,
		makeProdEdge(host, "bb-mirror", "app", "v1.0", image.Digest(digest)):  nil,
	}

	opts := &options.Options{
		SignImages:         true,
		MaxSignatureCopies: 10,
	}

	// Should succeed and report "All signatures already replicated".
	err := di.ReplicateSignatures(opts, edges)
	require.NoError(t, err)
}

// TestReplicateSignaturesBatchNoSignature verifies that edges without a
// signature on the primary are gracefully skipped.
func TestReplicateSignaturesBatchNoSignature(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	primary := prodPath("aa-primary")
	mirror := prodPath("bb-mirror")

	// Push images but NO signatures.
	digest := pushTestImage(t, di, host+"/"+primary+"/app:v1.0")
	pushTestImage(t, di, host+"/"+mirror+"/app:v1.0")

	edges := map[promotion.Edge]any{
		makeProdEdge(host, "aa-primary", "app", "v1.0", image.Digest(digest)): nil,
		makeProdEdge(host, "bb-mirror", "app", "v1.0", image.Digest(digest)):  nil,
	}

	opts := &options.Options{
		SignImages:         true,
		MaxSignatureCopies: 10,
	}

	// Should succeed — no signatures to copy.
	err := di.ReplicateSignatures(opts, edges)
	require.NoError(t, err)

	// Verify no signature appeared on the mirror.
	sigTag := digestToSignatureTag(image.Digest(digest))
	refStr := fmt.Sprintf("%s/%s/app:%s", host, mirror, sigTag)
	ref, err := name.ParseReference(refStr)
	require.NoError(t, err)

	_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
	require.Error(t, err, "signature should NOT exist on mirror")
}

// TestReplicateSignaturesBatchMultipleImages verifies batch listing across
// different image repositories within the same registries.
func TestReplicateSignaturesBatchMultipleImages(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	primary := prodPath("aa-primary")
	mirror := prodPath("bb-mirror")

	// Two different images with different digests.
	d1 := pushTestImage(t, di, host+"/"+primary+"/app:v1.0")
	d2 := pushTestImage(t, di, host+"/"+primary+"/web:latest")

	// Push signatures to primary only.
	sigTag1 := digestToSignatureTag(image.Digest(d1))
	sigTag2 := digestToSignatureTag(image.Digest(d2))

	pushTestImage(t, di, fmt.Sprintf("%s/%s/app:%s", host, primary, sigTag1))
	pushTestImage(t, di, fmt.Sprintf("%s/%s/web:%s", host, primary, sigTag2))

	// Push images to mirror (no signatures).
	pushTestImage(t, di, host+"/"+mirror+"/app:v1.0")
	pushTestImage(t, di, host+"/"+mirror+"/web:latest")

	edges := map[promotion.Edge]any{
		makeProdEdge(host, "aa-primary", "app", "v1.0", image.Digest(d1)):   nil,
		makeProdEdge(host, "bb-mirror", "app", "v1.0", image.Digest(d1)):    nil,
		makeProdEdge(host, "aa-primary", "web", "latest", image.Digest(d2)): nil,
		makeProdEdge(host, "bb-mirror", "web", "latest", image.Digest(d2)):  nil,
	}

	opts := &options.Options{
		SignImages:         true,
		MaxSignatureCopies: 10,
	}

	err := di.ReplicateSignatures(opts, edges)
	require.NoError(t, err)

	// Verify both signatures landed on the mirror.
	for _, tc := range []struct {
		img    string
		sigTag string
	}{
		{"app", sigTag1},
		{"web", sigTag2},
	} {
		refStr := fmt.Sprintf("%s/%s/%s:%s", host, mirror, tc.img, tc.sigTag)
		ref, err := name.ParseReference(refStr)
		require.NoError(t, err)

		_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
		require.NoError(t, err, "signature for %s should exist on mirror", tc.img)
	}
}

// TestReplicateSignaturesBatchIdempotent verifies that running
// ReplicateSignatures twice produces the same result (idempotent).
func TestReplicateSignaturesBatchIdempotent(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	primary := prodPath("aa-primary")
	mirror := prodPath("bb-mirror")

	digest := pushTestImage(t, di, host+"/"+primary+"/app:v1.0")
	sigTag := digestToSignatureTag(image.Digest(digest))
	pushTestImage(t, di, fmt.Sprintf("%s/%s/app:%s", host, primary, sigTag))
	pushTestImage(t, di, host+"/"+mirror+"/app:v1.0")

	edges := map[promotion.Edge]any{
		makeProdEdge(host, "aa-primary", "app", "v1.0", image.Digest(digest)): nil,
		makeProdEdge(host, "bb-mirror", "app", "v1.0", image.Digest(digest)):  nil,
	}

	opts := &options.Options{
		SignImages:         true,
		MaxSignatureCopies: 10,
	}

	// Run twice — both should succeed.
	for i := range 2 {
		err := di.ReplicateSignatures(opts, edges)
		require.NoError(t, err, "run %d", i+1)
	}

	// Verify signature exists on mirror.
	refStr := fmt.Sprintf("%s/%s/app:%s", host, mirror, sigTag)
	ref, err := name.ParseReference(refStr)
	require.NoError(t, err)

	_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
	require.NoError(t, err, "signature should exist on mirror")
}

// TestReplicateSignaturesMixedSignedUnsigned verifies the two-phase listing:
// only mirror repos for signed groups are listed, unsigned groups are skipped
// entirely in phase 2.
func TestReplicateSignaturesMixedSignedUnsigned(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	primary := prodPath("aa-primary")
	mirror := prodPath("bb-mirror")

	// "app" has a signature, "web" does not.
	dApp := pushTestImage(t, di, host+"/"+primary+"/app:v1.0")
	dWeb := pushTestImage(t, di, host+"/"+primary+"/web:latest")

	sigTagApp := digestToSignatureTag(image.Digest(dApp))
	pushTestImage(t, di, fmt.Sprintf("%s/%s/app:%s", host, primary, sigTagApp))

	// Push images to the mirror (no signatures).
	pushTestImage(t, di, host+"/"+mirror+"/app:v1.0")
	pushTestImage(t, di, host+"/"+mirror+"/web:latest")

	edges := map[promotion.Edge]any{
		makeProdEdge(host, "aa-primary", "app", "v1.0", image.Digest(dApp)):   nil,
		makeProdEdge(host, "bb-mirror", "app", "v1.0", image.Digest(dApp)):    nil,
		makeProdEdge(host, "aa-primary", "web", "latest", image.Digest(dWeb)): nil,
		makeProdEdge(host, "bb-mirror", "web", "latest", image.Digest(dWeb)):  nil,
	}

	opts := &options.Options{
		SignImages:         true,
		MaxSignatureCopies: 10,
	}

	err := di.ReplicateSignatures(opts, edges)
	require.NoError(t, err)

	// "app" signature should be replicated to the mirror.
	refStr := fmt.Sprintf("%s/%s/app:%s", host, mirror, sigTagApp)
	ref, err := name.ParseReference(refStr)
	require.NoError(t, err)

	_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
	require.NoError(t, err, "app signature should exist on mirror")

	// "web" should have no signature on the mirror (none existed on primary).
	sigTagWeb := digestToSignatureTag(image.Digest(dWeb))
	refStr = fmt.Sprintf("%s/%s/web:%s", host, mirror, sigTagWeb)
	ref, err = name.ParseReference(refStr)
	require.NoError(t, err)

	_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
	require.Error(t, err, "web signature should NOT exist on mirror")
}

// TestComputeCopiesFromInventoryTwoPhase directly tests computeCopiesFromInventory
// to verify it produces the correct copies with the two-phase listing strategy.
func TestComputeCopiesFromInventoryTwoPhase(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	primary := prodPath("aa-primary")
	mirror1 := prodPath("bb-mirror1")
	mirror2 := prodPath("cc-mirror2")

	// Push three images: img1 and img2 are signed, img3 is not.
	d1 := pushTestImage(t, di, host+"/"+primary+"/img1:v1")
	d2 := pushTestImage(t, di, host+"/"+primary+"/img2:v1")
	d3 := pushTestImage(t, di, host+"/"+primary+"/img3:v1")

	sigTag1 := digestToSignatureTag(image.Digest(d1))
	sigTag2 := digestToSignatureTag(image.Digest(d2))

	pushTestImage(t, di, fmt.Sprintf("%s/%s/img1:%s", host, primary, sigTag1))
	pushTestImage(t, di, fmt.Sprintf("%s/%s/img2:%s", host, primary, sigTag2))

	// Push images to mirrors (no signatures).
	for _, m := range []string{mirror1, mirror2} {
		pushTestImage(t, di, host+"/"+m+"/img1:v1")
		pushTestImage(t, di, host+"/"+m+"/img2:v1")
		pushTestImage(t, di, host+"/"+m+"/img3:v1")
	}

	// img2 signature already exists on mirror1 (partially replicated).
	pushTestImage(t, di, fmt.Sprintf("%s/%s/img2:%s", host, mirror1, sigTag2))

	// Build edges for all three images across all three registries.
	edges := map[promotion.Edge]any{}

	for _, img := range []struct {
		name   string
		tag    image.Tag
		digest image.Digest
	}{
		{"img1", "v1", image.Digest(d1)},
		{"img2", "v1", image.Digest(d2)},
		{"img3", "v1", image.Digest(d3)},
	} {
		for _, prefix := range []string{"aa-primary", "bb-mirror1", "cc-mirror2"} {
			edges[makeProdEdge(host, prefix, img.name, img.tag, img.digest)] = nil
		}
	}

	multiGroups := collectMultiRegistryGroups(edges)

	copies, err := di.computeCopiesFromInventory(multiGroups)
	require.NoError(t, err)

	// Expected copies:
	// - img1 signature → mirror1 and mirror2 (2 copies)
	// - img2 signature → mirror2 only (mirror1 already has it) (1 copy)
	// - img3 → no signature, no copies
	require.Len(t, copies, 3)

	// Verify the exact copy destinations.
	dsts := make(map[string]struct{}, len(copies))
	for _, c := range copies {
		dsts[c.dst] = struct{}{}
	}

	require.Contains(t, dsts, fmt.Sprintf("%s/%s/img1:%s", host, mirror1, sigTag1))
	require.Contains(t, dsts, fmt.Sprintf("%s/%s/img1:%s", host, mirror2, sigTag1))
	require.Contains(t, dsts, fmt.Sprintf("%s/%s/img2:%s", host, mirror2, sigTag2))
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
		MaxSignatureCopies: 10,
		MaxSignatureOps:    10,
	}

	gen := &fakeGenerator{data: []byte(`{"test": "attestation"}`)}

	// Run twice — both should succeed without error.
	for i := range 2 {
		err := di.WriteProvenanceAttestations(opts, edges, gen)
		require.NoError(t, err, "run %d", i+1)
	}

	// Verify the attestation exists with the correct predicate type.
	digestRef, err := name.NewDigest(fmt.Sprintf("%s/app@%s", dstRegistry, digest))
	require.NoError(t, err)

	remoteOpt := ociremote.WithRemoteOptions(remote.WithTransport(di.getTransport()))
	require.True(t, hasPredicateType(digestRef, provenance.PredicateType, remoteOpt),
		"attestation should exist in production")
}
