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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	cosignoci "github.com/sigstore/cosign/v3/pkg/oci"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	"github.com/sigstore/cosign/v3/pkg/oci/walk"
	"github.com/stretchr/testify/require"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	reg "sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	emptyConfigMediaType = "application/vnd.oci.empty.v1+json"
	artifactTypeProfile  = "application/vnd.example.seccomp-profile.v1+json"
	helmConfigType       = "application/vnd.cncf.helm.config.v1+json"
	helmChartType        = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"

	// deprecatedArtifactManifest is the OCI artifact manifest media type
	// that was dropped before OCI 1.1 was released.
	deprecatedArtifactManifest = "application/vnd.oci.artifact.manifest.v1+json"
)

// rawTaggable pushes manifest bytes unchanged.
type rawTaggable struct {
	mediaType types.MediaType
	data      []byte
}

func (r *rawTaggable) RawManifest() ([]byte, error) {
	return r.data, nil
}

func (r *rawTaggable) MediaType() (types.MediaType, error) {
	return r.mediaType, nil
}

func descriptorFor(mediaType types.MediaType, data []byte) v1.Descriptor {
	sum := sha256.Sum256(data)

	return v1.Descriptor{
		MediaType: mediaType,
		Size:      int64(len(data)),
		Digest:    v1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(sum[:])},
	}
}

// pushRawManifest pushes manifest to repo, tagged with tag or, if tag is
// empty, by digest only. It returns the manifest descriptor.
func pushRawManifest(
	t *testing.T, di *DefaultPromoterImplementation, repo, tag string, mediaType types.MediaType, manifest any,
) v1.Descriptor {
	t.Helper()

	data, err := json.Marshal(manifest)
	require.NoError(t, err)

	desc := descriptorFor(mediaType, data)

	r, err := name.NewRepository(repo)
	require.NoError(t, err)

	var ref name.Reference = r.Digest(desc.Digest.String())
	if tag != "" {
		ref = r.Tag(tag)
	}

	require.NoError(t, remote.Put(ref, &rawTaggable{mediaType: mediaType, data: data},
		remote.WithTransport(di.getTransport())))

	return desc
}

// pushTestArtifact pushes an OCI 1.1 artifact with the given config and a
// single layer to repo and returns its manifest descriptor.
func pushTestArtifact(
	t *testing.T, di *DefaultPromoterImplementation, repo, tag, artifactType string,
	configType types.MediaType, config []byte, layerType types.MediaType, layer []byte,
) v1.Descriptor {
	t.Helper()

	r, err := name.NewRepository(repo)
	require.NoError(t, err)

	for _, b := range []struct {
		mediaType types.MediaType
		data      []byte
	}{{configType, config}, {layerType, layer}} {
		require.NoError(t, remote.WriteLayer(r, static.NewLayer(b.data, b.mediaType),
			remote.WithTransport(di.getTransport())))
	}

	return pushRawManifest(t, di, repo, tag, types.OCIManifestSchema1, v1.Manifest{
		SchemaVersion: 2,
		MediaType:     types.OCIManifestSchema1,
		ArtifactType:  artifactType,
		Config:        descriptorFor(configType, config),
		Layers:        []v1.Descriptor{descriptorFor(layerType, layer)},
	})
}

// artifactFixture is a promotable object in the staging registry.
type artifactFixture struct {
	name   image.Name
	tag    image.Tag
	digest string
}

// pushArtifactFixtures pushes one object of each artifact shape to srcRepo:
// an empty config artifact, a Helm chart and an index of per-platform
// artifacts.
func pushArtifactFixtures(t *testing.T, di *DefaultPromoterImplementation, srcRegistry string) []artifactFixture {
	t.Helper()

	profile := pushTestArtifact(t, di, srcRegistry+"/profiles/runc", "v1.5.1", artifactTypeProfile,
		emptyConfigMediaType, []byte("{}"), "application/json", []byte(`{"defaultAction":"SCMP_ACT_ERRNO"}`))

	chart := pushTestArtifact(t, di, srcRegistry+"/charts/spo", "1.0.1", "",
		helmConfigType, []byte(`{"name":"spo","version":"1.0.1"}`), helmChartType, []byte("chart"))

	index := v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     types.OCIImageIndex,
	}

	for _, arch := range []string{"amd64", "arm64"} {
		child := pushTestArtifact(t, di, srcRegistry+"/spoc", "", "application/vnd.example.binary",
			emptyConfigMediaType, []byte("{}"), "application/vnd.example.binary", []byte("spoc "+arch))
		child.Platform = &v1.Platform{OS: "linux", Architecture: arch}
		index.Manifests = append(index.Manifests, child)
	}

	spoc := pushRawManifest(t, di, srcRegistry+"/spoc", "v1.0.1", types.OCIImageIndex, index)

	return []artifactFixture{
		{name: "profiles/runc", tag: "v1.5.1", digest: profile.Digest.String()},
		{name: "charts/spo", tag: "1.0.1", digest: chart.Digest.String()},
		{name: "spoc", tag: "v1.0.1", digest: spoc.Digest.String()},
	}
}

func artifactEdges(host string, fixtures []artifactFixture) map[promotion.Edge]any {
	edges := map[promotion.Edge]any{}

	for _, f := range fixtures {
		edges[promotion.Edge{
			SrcRegistry: reg.Context{Name: image.Registry(host + "/staging"), Src: true},
			SrcImageTag: promotion.ImageTag{Name: f.name, Tag: f.tag},
			Digest:      image.Digest(f.digest),
			DstRegistry: reg.Context{Name: image.Registry(host + "/production")},
			DstImageTag: promotion.ImageTag{Name: f.name, Tag: f.tag},
		}] = nil
	}

	return edges
}

// TestPromoteImagesArtifacts promotes OCI 1.1 artifacts through the full
// promote phase and checks that the manifests and all index children
// arrive unchanged.
func TestPromoteImagesArtifacts(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)
	di.SetRegistryProvider(reg.NewCraneProvider(reg.WithTransport(di.getTransport())))

	fixtures := pushArtifactFixtures(t, di, host+"/staging")

	require.NoError(t, di.PromoteImages(context.Background(), &options.Options{Threads: 2}, artifactEdges(host, fixtures)))

	get := func(ref string) *remote.Descriptor {
		r, err := name.ParseReference(ref)
		require.NoError(t, err)

		desc, err := remote.Get(r, remote.WithTransport(di.getTransport()))
		require.NoError(t, err, ref)

		return desc
	}

	for _, f := range fixtures {
		src := get(fmt.Sprintf("%s/staging/%s@%s", host, f.name, f.digest))
		dst := get(fmt.Sprintf("%s/production/%s:%s", host, f.name, f.tag))
		require.Equal(t, f.digest, dst.Digest.String(), f.name)
		require.True(t, bytes.Equal(src.Manifest, dst.Manifest), "%s manifest must be unchanged", f.name)

		if !dst.MediaType.IsIndex() {
			continue
		}

		idx, err := dst.ImageIndex()
		require.NoError(t, err)

		manifest, err := idx.IndexManifest()
		require.NoError(t, err)

		for _, child := range manifest.Manifests {
			got := get(fmt.Sprintf("%s/production/%s@%s", host, f.name, child.Digest))
			require.Equal(t, child.Digest, got.Digest)
		}
	}
}

// TestWriteProvenanceAttestationsArtifacts attaches promotion attestations
// to artifacts, which works like for images: the referrer is bound to the
// artifact digest.
func TestWriteProvenanceAttestationsArtifacts(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	// Attestations attach to the promoted objects.
	fixtures := pushArtifactFixtures(t, di, host+"/production")

	gen := &recordingGenerator{}
	signer := &fakeStatementSigner{bundle: []byte(`{"test": "bundle"}`)}
	di.attSigner = signer

	opts := &options.Options{SignImages: true, MaxSignatureOps: 10}
	require.NoError(t, di.WriteProvenanceAttestations(context.Background(), opts, nil, artifactEdges(host, fixtures), gen))

	require.Len(t, gen.records, len(fixtures), "one attestation per artifact")
	require.Equal(t, len(fixtures), signer.calls)

	for _, f := range fixtures {
		digestRef, err := name.NewDigest(fmt.Sprintf("%s/production/%s@%s", host, f.name, f.digest))
		require.NoError(t, err)

		require.True(t, di.hasBundleForPredicate(digestRef, provenance.PredicateType), f.name)
		require.False(t, di.hasBundleForPredicate(digestRef, "https://slsa.dev/provenance/v1"),
			"%s must only carry the promotion attestation", f.name)
	}
}

// TestCopyAttachedObjectsArtifact carries a staging signature of an
// artifact index to production.
func TestCopyAttachedObjectsArtifact(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	fixtures := pushArtifactFixtures(t, di, host+"/staging")
	spoc := fixtures[2]

	sigTag := digestToSignatureTag(image.Digest(spoc.digest))
	pushTestImage(t, di, fmt.Sprintf("%s/staging/%s:%s", host, spoc.name, sigTag))

	for edge := range artifactEdges(host, fixtures[2:]) {
		require.NoError(t, di.copyAttachedObjects(&edge))
	}

	ref, err := name.ParseReference(fmt.Sprintf("%s/production/%s:%s", host, spoc.name, sigTag))
	require.NoError(t, err)

	_, err = remote.Head(ref, remote.WithTransport(di.getTransport()))
	require.NoError(t, err, "signature should exist in production")
}

// walkForSigning visits every entity of ref the way recursive signing
// does (cosign sign --recursive, which the sign phase enables).
func walkForSigning(t *testing.T, di *DefaultPromoterImplementation, ref string) (int, error) {
	t.Helper()

	r, err := name.ParseReference(ref)
	require.NoError(t, err)

	opt := ociremote.WithRemoteOptions(remote.WithTransport(di.getTransport()))

	se, err := ociremote.SignedEntity(r, opt)
	if err != nil {
		return 0, fmt.Errorf("getting signed entity: %w", err)
	}

	visited := 0

	err = walk.SignedEntity(context.Background(), se, func(_ context.Context, _ cosignoci.SignedEntity) error {
		visited++

		return nil
	})
	if err != nil {
		return visited, fmt.Errorf("walking signed entity: %w", err)
	}

	return visited, nil
}

// TestRecursiveSigningWalkArtifacts checks that the walk recursive signing
// uses accepts artifacts and indexes of artifacts. It also documents that
// an index child with an unsupported media type is copied by the promote
// phase, and only fails afterwards, when signing walks the index.
func TestRecursiveSigningWalkArtifacts(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)
	di.SetRegistryProvider(reg.NewCraneProvider(reg.WithTransport(di.getTransport())))

	fixtures := pushArtifactFixtures(t, di, host+"/staging")

	for _, f := range fixtures {
		visited, err := walkForSigning(t, di, fmt.Sprintf("%s/staging/%s@%s", host, f.name, f.digest))
		require.NoError(t, err, f.name)
		require.Positive(t, visited, f.name)
	}

	// An index whose child uses the deprecated OCI artifact manifest.
	artifactManifest := map[string]any{
		"mediaType":    deprecatedArtifactManifest,
		"artifactType": artifactTypeProfile,
	}
	child := pushRawManifest(t, di, host+"/staging/legacy", "", deprecatedArtifactManifest, artifactManifest)

	// go-containerregistry copies index children of unknown media types as
	// blobs. Registries that store manifests as blobs, like Artifact
	// Registry, serve them that way; the in-memory registry doesn't, so the
	// child is pushed as a blob as well.
	childData, err := json.Marshal(artifactManifest)
	require.NoError(t, err)

	legacyRepo, err := name.NewRepository(host + "/staging/legacy")
	require.NoError(t, err)
	require.NoError(t, remote.WriteLayer(legacyRepo, static.NewLayer(childData, deprecatedArtifactManifest),
		remote.WithTransport(di.getTransport())))

	legacy := pushRawManifest(t, di, host+"/staging/legacy", "v1", types.OCIImageIndex, v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     types.OCIImageIndex,
		Manifests:     []v1.Descriptor{child},
	})

	edges := artifactEdges(host, []artifactFixture{{name: "legacy", tag: "v1", digest: legacy.Digest.String()}})
	require.NoError(t, di.PromoteImages(context.Background(), &options.Options{Threads: 1}, edges),
		"the promote phase copies the index")

	_, err = walkForSigning(t, di, host+"/production/legacy:v1")
	require.ErrorContains(t, err, "unknown mime type: "+deprecatedArtifactManifest,
		"signing fails only after the copy, see kubernetes-sigs/promo-tools#1957")
}
