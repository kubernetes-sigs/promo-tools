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

package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	// artifactTypeSeccomp is a custom OCI 1.1 artifact type, as used for
	// the Security Profiles Operator seccomp profiles.
	artifactTypeSeccomp = "application/vnd.example.seccomp-profile.v1+json"

	helmConfigMediaType = "application/vnd.cncf.helm.config.v1+json"
	helmChartMediaType  = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"

	// largeLayerSize is the size of the single layer in the large artifact
	// test. The in-memory registries keep every blob in memory, so the
	// test needs about five times this amount of memory.
	largeLayerSize = 300 << 20

	// largeLayerSizeRace replaces largeLayerSize with the race detector,
	// which needs about 20 times the memory. It keeps the unit test job
	// within its memory limit while still streaming a multi chunk blob.
	largeLayerSizeRace = 32 << 20
)

// rawManifest is a manifest pushed byte for byte, so tests control every
// field, including media types go-containerregistry does not model.
type rawManifest struct {
	mediaType types.MediaType
	data      []byte
}

func (r *rawManifest) RawManifest() ([]byte, error) {
	return r.data, nil
}

func (r *rawManifest) MediaType() (types.MediaType, error) {
	return r.mediaType, nil
}

// blob is content that a manifest references.
type blob struct {
	mediaType types.MediaType
	data      []byte
}

// sha256Hash converts a SHA-256 sum to a digest.
func sha256Hash(sum []byte) v1.Hash {
	return v1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(sum)}
}

func (b blob) descriptor() v1.Descriptor {
	sum := sha256.Sum256(b.data)

	return v1.Descriptor{
		MediaType: b.mediaType,
		Size:      int64(len(b.data)),
		Digest:    sha256Hash(sum[:]),
	}
}

// pushBlob uploads a blob to the repository of ref.
func pushBlob(t *testing.T, repo name.Repository, b blob) {
	t.Helper()

	desc := b.descriptor()
	layer := &generatedLayer{
		mediaType: b.mediaType,
		size:      desc.Size,
		digest:    desc.Digest,
		open: func() io.Reader {
			return strings.NewReader(string(b.data))
		},
	}

	require.NoError(t, remote.WriteLayer(repo, layer))
}

// pushManifest pushes a raw manifest to ref and returns its descriptor.
// A ref without tag or digest pushes the manifest by its digest only.
func pushManifest(t *testing.T, ref string, mediaType types.MediaType, manifest any) v1.Descriptor {
	t.Helper()

	data, err := json.Marshal(manifest)
	require.NoError(t, err)

	sum := sha256.Sum256(data)
	desc := v1.Descriptor{
		MediaType: mediaType,
		Size:      int64(len(data)),
		Digest:    sha256Hash(sum[:]),
	}

	var r name.Reference

	if repo, err := name.NewRepository(ref); err == nil && !strings.ContainsAny(ref[strings.LastIndex(ref, "/")+1:], ":@") {
		r = repo.Digest(desc.Digest.String())
	} else {
		r, err = name.ParseReference(ref)
		require.NoError(t, err)
	}

	require.NoError(t, remote.Put(r, &rawManifest{mediaType: mediaType, data: data}))

	return desc
}

// pushArtifact pushes the config and layer blobs and an OCI image manifest
// referencing them to ref, see pushManifest. It returns the manifest
// descriptor.
func pushArtifact(
	t *testing.T, ref, artifactType string, config blob, layers []blob, subject *v1.Descriptor,
) v1.Descriptor {
	t.Helper()

	r, err := name.ParseReference(ref)
	require.NoError(t, err)

	pushBlob(t, r.Context(), config)

	manifest := v1.Manifest{
		SchemaVersion: 2,
		MediaType:     types.OCIManifestSchema1,
		ArtifactType:  artifactType,
		Config:        config.descriptor(),
		Subject:       subject,
	}

	for _, l := range layers {
		pushBlob(t, r.Context(), l)
		manifest.Layers = append(manifest.Layers, l.descriptor())
	}

	return pushManifest(t, ref, types.OCIManifestSchema1, manifest)
}

// emptyConfig is the OCI 1.1 empty descriptor content.
func emptyConfig() blob {
	return blob{mediaType: "application/vnd.oci.empty.v1+json", data: []byte("{}")}
}

// requireSameManifest asserts that dst serves the exact manifest bytes of
// src and returns the parsed manifest.
func requireSameManifest(t *testing.T, src, dst string) []byte {
	t.Helper()

	srcManifest, err := crane.Manifest(src, crane.Insecure)
	require.NoError(t, err)

	dstManifest, err := crane.Manifest(dst, crane.Insecure)
	require.NoError(t, err)

	require.Equal(t, string(srcManifest), string(dstManifest), "manifest must be copied byte for byte")

	return dstManifest
}

// requireBlobs asserts that all blobs of a manifest exist in repo.
func requireBlobs(t *testing.T, repo string, descs ...v1.Descriptor) {
	t.Helper()

	r, err := name.NewRepository(repo)
	require.NoError(t, err)

	for _, d := range descs {
		layer, err := remote.Layer(r.Digest(d.Digest.String()))
		require.NoError(t, err)

		size, err := layer.Size()
		require.NoError(t, err, "blob %s must exist in %s", d.Digest, repo)
		require.Equal(t, d.Size, size)
	}
}

func TestCraneProviderCopyEmptyConfigArtifact(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)
	provider := newInsecureCraneProvider()

	profile := blob{mediaType: "application/json", data: []byte(`{"defaultAction":"SCMP_ACT_ERRNO"}`)}
	src := host + "/staging/profiles/runc:v1.5.1"
	dst := host + "/prod/profiles/runc:v1.5.1"

	desc := pushArtifact(t, src, artifactTypeSeccomp, emptyConfig(), []blob{profile}, nil)

	require.NoError(t, provider.CopyImage(context.Background(), src, dst))

	raw := requireSameManifest(t, src, dst)

	var manifest v1.Manifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, artifactTypeSeccomp, manifest.ArtifactType)
	require.Equal(t, types.MediaType("application/vnd.oci.empty.v1+json"), manifest.Config.MediaType)

	digest, err := crane.Digest(dst, crane.Insecure)
	require.NoError(t, err)
	require.Equal(t, desc.Digest.String(), digest)

	requireBlobs(t, host+"/prod/profiles/runc", emptyConfig().descriptor(), profile.descriptor())
}

func TestCraneProviderCopyHelmChart(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)
	provider := newInsecureCraneProvider()

	config := blob{mediaType: helmConfigMediaType, data: []byte(`{"name":"security-profiles-operator","version":"1.0.1"}`)}
	chart := blob{mediaType: helmChartMediaType, data: []byte("not really a tarball, but content addressed all the same")}
	src := host + "/staging/charts/security-profiles-operator:1.0.1"
	dst := host + "/prod/charts/security-profiles-operator:1.0.1"

	pushArtifact(t, src, "", config, []blob{chart}, nil)

	require.NoError(t, provider.CopyImage(context.Background(), src, dst))

	raw := requireSameManifest(t, src, dst)

	var manifest v1.Manifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, types.MediaType(helmConfigMediaType), manifest.Config.MediaType)
	require.Len(t, manifest.Layers, 1)
	require.Equal(t, types.MediaType(helmChartMediaType), manifest.Layers[0].MediaType)

	requireBlobs(t, host+"/prod/charts/security-profiles-operator", config.descriptor(), chart.descriptor())
}

func TestCraneProviderCopyManifestWithSubject(t *testing.T) {
	t.Parallel()

	s := httptest.NewServer(registry.New(registry.WithReferrersSupport(true)))
	t.Cleanup(s.Close)

	host := s.Listener.Addr().String()
	provider := newInsecureCraneProvider()

	imgDigest := pushRandomImage(t, host+"/staging/app:v1.0")

	subjectRef, err := name.NewDigest(host + "/staging/app@" + imgDigest)
	require.NoError(t, err)

	subject, err := remote.Head(subjectRef)
	require.NoError(t, err)

	sbom := blob{mediaType: "application/spdx+json", data: []byte(`{"spdxVersion":"SPDX-2.3"}`)}
	desc := pushArtifact(t, host+"/staging/app:sbom", "application/spdx+json", emptyConfig(), []blob{sbom}, subject)

	// Promotion copies the image and, as its own edge, the referrer by digest.
	require.NoError(t, provider.CopyImage(context.Background(),
		host+"/staging/app@"+imgDigest, host+"/prod/app@"+imgDigest))
	require.NoError(t, provider.CopyImage(context.Background(),
		host+"/staging/app@"+desc.Digest.String(), host+"/prod/app@"+desc.Digest.String()))

	raw := requireSameManifest(t,
		host+"/staging/app@"+desc.Digest.String(), host+"/prod/app@"+desc.Digest.String())

	var manifest v1.Manifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.NotNil(t, manifest.Subject)
	require.Equal(t, imgDigest, manifest.Subject.Digest.String())

	// The copied referrer is discoverable from the promoted image.
	dstSubject, err := name.NewDigest(host + "/prod/app@" + imgDigest)
	require.NoError(t, err)

	referrers, err := remote.Referrers(dstSubject)
	require.NoError(t, err)

	idx, err := referrers.IndexManifest()
	require.NoError(t, err)
	require.Len(t, idx.Manifests, 1)
	require.Equal(t, desc.Digest, idx.Manifests[0].Digest)
}

func TestCraneProviderCopyArtifactIndex(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)
	provider := newInsecureCraneProvider()

	index := v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     types.OCIImageIndex,
		ArtifactType:  artifactTypeSeccomp,
	}

	arches := []string{"amd64", "arm64", "ppc64le", "s390x"}
	children := make([]v1.Descriptor, 0, len(arches))

	for _, arch := range arches {
		binary := blob{
			mediaType: "application/vnd.example.binary",
			data:      []byte("spoc for " + arch),
		}
		// Children are pushed by digest only, like buildx or oras do.
		desc := pushArtifact(t, host+"/staging/spoc",
			"application/vnd.example.binary", emptyConfig(), []blob{binary}, nil)
		desc.Platform = &v1.Platform{OS: "linux", Architecture: arch}
		index.Manifests = append(index.Manifests, desc)
		children = append(children, desc)
	}

	src := host + "/staging/spoc:v1.0.1"
	dst := host + "/prod/spoc:v1.0.1"

	idxDesc := pushManifest(t, src, types.OCIImageIndex, index)

	require.NoError(t, provider.CopyImage(context.Background(), src, dst))

	raw := requireSameManifest(t, src, dst)

	var copied v1.IndexManifest
	require.NoError(t, json.Unmarshal(raw, &copied))
	require.Equal(t, artifactTypeSeccomp, copied.ArtifactType)
	require.Len(t, copied.Manifests, len(children))

	digest, err := crane.Digest(dst, crane.Insecure)
	require.NoError(t, err)
	require.Equal(t, idxDesc.Digest.String(), digest)

	for _, child := range children {
		requireSameManifest(t,
			host+"/staging/spoc@"+child.Digest.String(), host+"/prod/spoc@"+child.Digest.String())
	}
}

// raceEnabled reports whether the test binary was built with the race
// detector, which multiplies the memory needed for large blobs.
func raceEnabled() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}

	for _, s := range info.Settings {
		if s.Key == "-race" {
			return s.Value == "true"
		}
	}

	return false
}

// generatedLayer is a layer whose content is produced on the fly, so large
// layers don't have to be held in memory by the test.
type generatedLayer struct {
	mediaType types.MediaType
	size      int64
	digest    v1.Hash
	open      func() io.Reader
}

func (g *generatedLayer) Digest() (v1.Hash, error) {
	return g.digest, nil
}

func (g *generatedLayer) DiffID() (v1.Hash, error) {
	return g.digest, nil
}

func (g *generatedLayer) Size() (int64, error) {
	return g.size, nil
}

func (g *generatedLayer) MediaType() (types.MediaType, error) {
	return g.mediaType, nil
}

func (g *generatedLayer) Compressed() (io.ReadCloser, error) {
	return io.NopCloser(g.open()), nil
}

func (g *generatedLayer) Uncompressed() (io.ReadCloser, error) {
	return io.NopCloser(g.open()), nil
}

// newRandomLayer returns a deterministic, incompressible layer of size bytes.
func newRandomLayer(t *testing.T, size int64, mediaType types.MediaType) *generatedLayer {
	t.Helper()

	open := func() io.Reader {
		//nolint:gosec // deterministic test content, not used for security
		return io.LimitReader(rand.New(rand.NewSource(1)), size)
	}

	h := sha256.New()
	_, err := io.Copy(h, open())
	require.NoError(t, err)

	return &generatedLayer{
		mediaType: mediaType,
		size:      size,
		digest:    sha256Hash(h.Sum(nil)),
		open:      open,
	}
}

func TestCraneProviderCopyLargeSingleLayerArtifact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large blob copy in short mode")
	}

	t.Parallel()

	host := newTestRegistry(t)
	provider := newInsecureCraneProvider()

	src := host + "/staging/kubernetes/kubelet:v1.37.0"
	dst := host + "/prod/kubernetes/kubelet:v1.37.0"

	r, err := name.ParseReference(src)
	require.NoError(t, err)

	size := int64(largeLayerSize)
	if raceEnabled() {
		size = largeLayerSizeRace
	}

	layer := newRandomLayer(t, size, "application/vnd.example.binary")
	require.NoError(t, remote.WriteLayer(r.Context(), layer))

	config := emptyConfig()
	pushBlob(t, r.Context(), config)

	pushManifest(t, src, types.OCIManifestSchema1, v1.Manifest{
		SchemaVersion: 2,
		MediaType:     types.OCIManifestSchema1,
		ArtifactType:  "application/vnd.example.binary",
		Config:        config.descriptor(),
		Layers: []v1.Descriptor{{
			MediaType: layer.mediaType,
			Size:      layer.size,
			Digest:    layer.digest,
		}},
	})

	require.NoError(t, provider.CopyImage(context.Background(), src, dst))

	requireSameManifest(t, src, dst)

	// Verify the content, not only the size, by hashing the copied blob.
	dstRepo, err := name.NewRepository(host + "/prod/kubernetes/kubelet")
	require.NoError(t, err)

	copied, err := remote.Layer(dstRepo.Digest(layer.digest.String()))
	require.NoError(t, err)

	rc, err := copied.Compressed()
	require.NoError(t, err)

	defer rc.Close()

	h := sha256.New()
	n, err := io.Copy(h, rc)
	require.NoError(t, err)
	require.Equal(t, layer.size, n)
	require.Equal(t, layer.digest.Hex, hex.EncodeToString(h.Sum(nil)))
}

// googleTagsHandler adds the Google Container Registry and Artifact
// Registry tags list extension to an OCI registry: the manifests of a
// repository with their tags and media types, and its child repositories.
// CraneProvider.ReadRegistries depends on this extension.
func googleTagsHandler(t *testing.T, next http.Handler) http.Handler {
	t.Helper()

	get := func(ctx context.Context, method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, method, path, nil))

		return rec
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		repo, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/v2/"), "/tags/list")
		if r.Method != http.MethodGet || !ok || !strings.HasPrefix(r.URL.Path, "/v2/") {
			next.ServeHTTP(w, r)

			return
		}

		type manifestInfo struct {
			Size      string   `json:"imageSizeBytes"`
			MediaType string   `json:"mediaType"`
			Created   string   `json:"timeCreatedMs"`
			Uploaded  string   `json:"timeUploadedMs"`
			Tags      []string `json:"tag"`
		}

		resp := struct {
			Children  []string                `json:"child"`
			Manifests map[string]manifestInfo `json:"manifest"`
			Name      string                  `json:"name"`
			Tags      []string                `json:"tags"`
		}{
			Children:  []string{},
			Manifests: map[string]manifestInfo{},
			Name:      repo,
			Tags:      []string{},
		}

		if rec := get(ctx, http.MethodGet, r.URL.Path); rec.Code == http.StatusOK {
			var list struct {
				Tags []string `json:"tags"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)

				return
			}

			resp.Tags = list.Tags

			for _, tag := range list.Tags {
				head := get(ctx, http.MethodHead, "/v2/"+repo+"/manifests/"+tag)
				digest := head.Header().Get("Docker-Content-Digest")
				info := resp.Manifests[digest]
				info.MediaType = head.Header().Get("Content-Type")
				info.Size, info.Created, info.Uploaded = "0", "0", "0"
				info.Tags = append(info.Tags, tag)
				resp.Manifests[digest] = info
			}
		}

		var catalog struct {
			Repositories []string `json:"repositories"`
		}

		rec := get(ctx, http.MethodGet, "/v2/_catalog?n=10000")
		if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		seen := map[string]bool{}

		for _, name := range catalog.Repositories {
			rest, ok := strings.CutPrefix(name, repo+"/")
			if !ok {
				continue
			}

			child, _, _ := strings.Cut(rest, "/")
			if !seen[child] {
				seen[child] = true
				resp.Children = append(resp.Children, child)
			}
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}

func TestCraneProviderReadRegistriesWithArtifacts(t *testing.T) {
	t.Parallel()

	s := httptest.NewServer(googleTagsHandler(t, registry.New()))
	t.Cleanup(s.Close)

	host := s.Listener.Addr().String()
	provider := NewCraneProvider()

	imgDigest := pushRandomImage(t, host+"/staging/api:v1.0")

	img, err := random.Index(512, 1, 2)
	require.NoError(t, err)

	idxRef, err := name.ParseReference(host + "/staging/nested/web:v3.0")
	require.NoError(t, err)
	require.NoError(t, remote.WriteIndex(idxRef, img))

	idxDigest, err := img.Digest()
	require.NoError(t, err)

	profile := blob{mediaType: "application/json", data: []byte(`{}`)}
	artifact := pushArtifact(t, host+"/staging/profiles/runc:v1.5.1",
		artifactTypeSeccomp, emptyConfig(), []blob{profile}, nil)

	chart := pushArtifact(t, host+"/staging/charts/spo:1.0.1", "",
		blob{mediaType: helmConfigMediaType, data: []byte(`{"name":"spo"}`)},
		[]blob{{mediaType: helmChartMediaType, data: []byte("chart")}}, nil)

	registries := []RegistryConfig{{Name: image.Registry(host + "/staging"), Src: true}}

	inv, err := provider.ReadRegistries(context.Background(), registries, true, nil)
	require.NoError(t, err)

	images := inv.Images[image.Registry(host+"/staging")]
	require.NotNil(t, images)

	expected := map[image.Name]struct {
		digest    string
		tag       image.Tag
		mediaType types.MediaType
	}{
		"api":           {imgDigest, "v1.0", types.DockerManifestSchema2},
		"nested/web":    {idxDigest.String(), "v3.0", types.OCIImageIndex},
		"profiles/runc": {artifact.Digest.String(), "v1.5.1", types.OCIManifestSchema1},
		"charts/spo":    {chart.Digest.String(), "1.0.1", types.OCIManifestSchema1},
	}

	require.Len(t, images, len(expected))

	for imgName, want := range expected {
		tags, ok := images[imgName][image.Digest(want.digest)]
		require.True(t, ok, "%s@%s must be in the inventory", imgName, want.digest)
		require.Equal(t, TagSlice{want.tag}, tags, imgName)
		require.Equal(t, want.mediaType, inv.MediaTypes[image.Digest(want.digest)], imgName)
	}

	// Without recursion, only the top level repository is read.
	flat, err := provider.ReadRegistries(context.Background(),
		[]RegistryConfig{{Name: image.Registry(host + "/staging/api"), Src: true}}, false, nil)
	require.NoError(t, err)

	apiImages := flat.Images[image.Registry(host+"/staging/api")]
	require.Len(t, apiImages, 1)
	require.Contains(t, apiImages[""], image.Digest(imgDigest), "%v", apiImages)
}
