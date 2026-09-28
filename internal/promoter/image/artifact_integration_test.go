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
	"maps"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
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
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry/registryfakes"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/promoter/image/vuln"
	"sigs.k8s.io/promo-tools/v4/promoter/image/vuln/vulnfakes"
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

// pushLegacyIndex pushes an index whose child uses the deprecated OCI
// artifact manifest to repo and returns the index and child descriptors.
func pushLegacyIndex(t *testing.T, di *DefaultPromoterImplementation, repo, tag string) (v1.Descriptor, v1.Descriptor) {
	t.Helper()

	child := pushRawManifest(t, di, repo, "", deprecatedArtifactManifest, map[string]any{
		"mediaType":    deprecatedArtifactManifest,
		"artifactType": artifactTypeProfile,
	})

	index := pushRawManifest(t, di, repo, tag, types.OCIImageIndex, v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     types.OCIImageIndex,
		Manifests:     []v1.Descriptor{child},
	})

	return index, child
}

// getDescriptor returns the manifest descriptor of ref.
func getDescriptor(t *testing.T, di *DefaultPromoterImplementation, ref string) v1.Descriptor {
	t.Helper()

	r, err := name.ParseReference(ref)
	require.NoError(t, err)

	desc, err := remote.Get(r, remote.WithTransport(di.getTransport()))
	require.NoError(t, err)

	return desc.Descriptor
}

// planArtifacts runs GetPromotionEdges for fixtures in the staging
// registry of host. The in-memory registry doesn't serve the Google tags
// list extension, so the inventory comes from a fake provider, with the
// given media types. Manifests are read from the registry.
func planArtifacts(
	t *testing.T, di *DefaultPromoterImplementation, host string, fixtures []artifactFixture,
	mediaTypes map[image.Digest]types.MediaType,
) (map[promotion.Edge]any, error) {
	t.Helper()

	src := reg.Context{Name: image.Registry(host + "/staging"), Src: true}
	mfest := schema.Manifest{
		Registries:  []reg.Context{src, {Name: image.Registry(host + "/production")}},
		SrcRegistry: &src,
	}

	inv := reg.NewInventory()
	inv.Images[src.Name] = reg.RegInvImage{}
	maps.Copy(inv.MediaTypes, mediaTypes)

	for _, f := range fixtures {
		if _, ok := inv.Images[src.Name][f.name]; !ok {
			inv.Images[src.Name][f.name] = reg.DigestTags{}
		}

		inv.Images[src.Name][f.name][image.Digest(f.digest)] = reg.TagSlice{f.tag}
	}

	for imgName, dmap := range inv.Images[src.Name] {
		mfest.Images = append(mfest.Images, reg.Image{Name: imgName, Dmap: dmap})
	}

	provider := &registryfakes.FakeProvider{}
	provider.ReadRegistriesReturns(inv, nil)
	di.SetRegistryProvider(provider)

	return di.GetPromotionEdges(context.Background(), &options.Options{}, []schema.Manifest{mfest})
}

// TestGetPromotionEdgesArtifacts checks that planning accepts images,
// artifacts and (nested) indexes of them.
func TestGetPromotionEdgesArtifacts(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	fixtures := pushArtifactFixtures(t, di, host+"/staging")

	app := pushTestImage(t, di, host+"/staging/app:v1")
	fixtures = append(fixtures, artifactFixture{name: testImageApp, tag: "v1", digest: app})

	// An index of the spoc index.
	spoc := fixtures[2]
	nested := pushRawManifest(t, di, host+"/staging/spoc", "nested", types.OCIImageIndex, v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     types.OCIImageIndex,
		Manifests:     []v1.Descriptor{getDescriptor(t, di, fmt.Sprintf("%s/staging/spoc@%s", host, spoc.digest))},
	})
	fixtures = append(fixtures, artifactFixture{name: "spoc", tag: "nested", digest: nested.Digest.String()})

	// Image manifests known from the inventory aren't fetched.
	missing := "sha256:" + strings.Repeat("0", 64)
	fixtures = append(fixtures, artifactFixture{name: "missing", tag: "v1", digest: missing})

	edges, err := planArtifacts(t, di, host, fixtures, map[image.Digest]types.MediaType{
		image.Digest(missing): types.OCIManifestSchema1,
	})
	require.NoError(t, err)
	require.Len(t, edges, len(fixtures))
}

// TestGetPromotionEdgesUnsupportedArtifacts checks that planning rejects
// objects recursive signing can't walk, and reports all of them.
func TestGetPromotionEdgesUnsupportedArtifacts(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	fixtures := pushArtifactFixtures(t, di, host+"/staging")

	// A deprecated OCI artifact manifest.
	top := pushRawManifest(t, di, host+"/staging/top", "v1", deprecatedArtifactManifest, map[string]any{
		"mediaType":    deprecatedArtifactManifest,
		"artifactType": artifactTypeProfile,
	})

	// An index with a deprecated OCI artifact manifest child.
	legacy, legacyChild := pushLegacyIndex(t, di, host+"/staging/legacy", "v1")

	// A nested index with a layer as grandchild and another one as child,
	// both reported.
	layer := descriptorFor(helmChartType, []byte("chart"))
	sibling := descriptorFor(helmChartType, []byte("sibling"))
	inner := pushRawManifest(t, di, host+"/staging/layer", "", types.OCIImageIndex, v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     types.OCIImageIndex,
		Manifests:     []v1.Descriptor{layer},
	})
	outer := pushRawManifest(t, di, host+"/staging/layer", "v1", types.OCIImageIndex, v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     types.OCIImageIndex,
		Manifests:     []v1.Descriptor{inner, sibling},
	})

	fixtures = append(fixtures,
		artifactFixture{name: "top", tag: "v1", digest: top.Digest.String()},
		artifactFixture{name: "legacy", tag: "v1", digest: legacy.Digest.String()},
		artifactFixture{name: "layer", tag: "v1", digest: outer.Digest.String()},
	)

	_, err := planArtifacts(t, di, host, fixtures, nil)
	require.ErrorIs(t, err, errUnsupportedMediaType)
	require.ErrorContains(t, err, fmt.Sprintf("image %s/staging/top@%s: media type %q",
		host, top.Digest, deprecatedArtifactManifest))
	require.ErrorContains(t, err, fmt.Sprintf("image %s/staging/legacy@%s: index child %s has media type %q",
		host, legacy.Digest, legacyChild.Digest, deprecatedArtifactManifest))
	require.ErrorContains(t, err, fmt.Sprintf("image %s/staging/layer@%s: index child %s has media type %q",
		host, outer.Digest, layer.Digest, helmChartType))
	require.ErrorContains(t, err, fmt.Sprintf("image %s/staging/layer@%s: index child %s has media type %q",
		host, outer.Digest, sibling.Digest, helmChartType))

	for _, f := range fixtures[:3] {
		require.NotContains(t, err.Error(), f.digest, "%s is supported", f.name)
	}
}

// TestRecursiveSigningWalkArtifacts checks that the walk recursive signing
// uses accepts artifacts and indexes of artifacts. An index child with an
// unsupported media type fails the walk, so planning rejects such an index
// before the promote phase copies it.
func TestRecursiveSigningWalkArtifacts(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	fixtures := pushArtifactFixtures(t, di, host+"/staging")

	for _, f := range fixtures {
		visited, err := walkForSigning(t, di, fmt.Sprintf("%s/staging/%s@%s", host, f.name, f.digest))
		require.NoError(t, err, f.name)
		require.Positive(t, visited, f.name)
	}

	// An index whose child uses the deprecated OCI artifact manifest.
	legacy, child := pushLegacyIndex(t, di, host+"/staging/legacy", "v1")

	_, err := walkForSigning(t, di, host+"/staging/legacy:v1")
	require.ErrorContains(t, err, "unknown mime type: "+deprecatedArtifactManifest)

	_, err = planArtifacts(t, di, host, []artifactFixture{{name: "legacy", tag: "v1", digest: legacy.Digest.String()}}, nil)
	require.ErrorIs(t, err, errUnsupportedMediaType)
	require.ErrorContains(t, err, fmt.Sprintf("image %s/staging/legacy@%s: index child %s has media type %q",
		host, legacy.Digest, child.Digest, deprecatedArtifactManifest),
		"planning rejects the index before it is copied and partially signed")
}

// TestScanEdgesArtifacts checks that vulnerability scans skip artifacts and
// indexes of artifacts as not applicable, but scan images and indexes of
// images.
func TestScanEdgesArtifacts(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	fixtures := pushArtifactFixtures(t, di, host+"/staging")

	app := pushTestImage(t, di, host+"/staging/app:v1")

	idx, err := random.Index(1024, 1, 2)
	require.NoError(t, err)

	multiRef, err := name.ParseReference(host + "/staging/multi:v1")
	require.NoError(t, err)
	require.NoError(t, remote.WriteIndex(multiRef, idx, remote.WithTransport(di.getTransport())))

	multi, err := idx.Digest()
	require.NoError(t, err)

	fixtures = append(fixtures,
		artifactFixture{name: testImageApp, tag: "v1", digest: app},
		artifactFixture{name: "multi", tag: "v1", digest: multi.String()},
	)

	scanner := &vulnfakes.FakeScanner{}
	scanner.ScanReturns(&vuln.ScanResult{}, nil)
	di.SetVulnScanner(scanner)

	opts := &options.Options{SeverityThreshold: int(vuln.SeverityHigh)}
	require.NoError(t, di.ScanEdges(context.Background(), opts, artifactEdges(host, fixtures)))

	scanned := make([]string, 0, scanner.ScanCallCount())

	for i := range scanner.ScanCallCount() {
		_, ref := scanner.ScanArgsForCall(i)
		scanned = append(scanned, ref)
	}

	require.ElementsMatch(t, []string{
		fmt.Sprintf("%s/staging/app@%s", host, app),
		fmt.Sprintf("%s/staging/multi@%s", host, multi),
	}, scanned)
}
