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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

// newTestRegistry starts an in-process OCI registry and returns its
// host:port address along with a cleanup function.
func newTestRegistry(t *testing.T) string {
	t.Helper()

	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)

	return s.Listener.Addr().String()
}

// pushRandomImage creates a minimal random image and pushes it to the
// given reference on the local registry. It returns the pushed image digest.
func pushRandomImage(t *testing.T, ref string) string {
	t.Helper()

	img, err := random.Image(1024, 1)
	require.NoError(t, err)

	err = crane.Push(img, ref, crane.Insecure)
	require.NoError(t, err)

	digest, err := crane.Digest(ref, crane.Insecure)
	require.NoError(t, err)

	return digest
}

// newInsecureCraneProvider creates a CraneProvider configured for plain HTTP
// registries.
func newInsecureCraneProvider() *CraneProvider {
	return NewCraneProvider(WithCraneOptions(crane.Insecure))
}

func TestCraneProviderCopyImageByTag(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)

	srcRef := host + "/staging/myimage:v1.0"
	srcDigest := pushRandomImage(t, srcRef)

	p := newInsecureCraneProvider()
	dstRef := host + "/production/myimage:v1.0"
	err := p.CopyImage(context.Background(), srcRef, dstRef)
	require.NoError(t, err)

	// Verify the destination image exists and has the same digest.
	dstDigest, err := crane.Digest(dstRef, crane.Insecure)
	require.NoError(t, err)
	require.Equal(t, srcDigest, dstDigest)
}

func TestCraneProviderCopyByDigest(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)

	// Push an image by tag so it has a manifest in the registry.
	tagRef := host + "/staging/myimage:v1.0"
	digest := pushRandomImage(t, tagRef)

	// Copy using a digest reference (FQIN → FQIN).
	// This is also the tagless promotion code path when edge.DstImageTag.Tag == "".
	p := newInsecureCraneProvider()
	srcByDigest := host + "/staging/myimage@" + digest
	dstByDigest := host + "/production/myimage@" + digest
	err := p.CopyImage(context.Background(), srcByDigest, dstByDigest)
	require.NoError(t, err)

	// Verify the destination digest matches.
	gotDigest, err := crane.Digest(dstByDigest, crane.Insecure)
	require.NoError(t, err)
	require.Equal(t, digest, gotDigest)
}

func TestCraneProviderCopyMultipleTagsSameDigest(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)

	// Push one image and tag it.
	srcRef := host + "/staging/myimage:v1.0"
	digest := pushRandomImage(t, srcRef)

	p := newInsecureCraneProvider()

	// Promote the same digest to multiple tags in destination, simulating
	// a manifest with dmap: {"sha256:...": ["v1.0", "latest"]}.
	srcVertex := host + "/staging/myimage@" + digest
	for _, tag := range []string{"v1.0", "latest"} {
		dstVertex := fmt.Sprintf("%s/production/myimage:%s", host, tag)
		err := p.CopyImage(context.Background(), srcVertex, dstVertex)
		require.NoError(t, err)
	}

	// Both tags should resolve to the same digest.
	for _, tag := range []string{"v1.0", "latest"} {
		ref := fmt.Sprintf("%s/production/myimage:%s", host, tag)
		gotDigest, err := crane.Digest(ref, crane.Insecure)
		require.NoError(t, err)
		require.Equal(t, digest, gotDigest, "tag %s has wrong digest", tag)
	}

	tags, err := crane.ListTags(host+"/production/myimage", crane.Insecure)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"v1.0", "latest"}, tags)
}

func TestCraneProviderCopyNonexistentSource(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)

	p := newInsecureCraneProvider()
	err := p.CopyImage(
		context.Background(),
		host+"/staging/doesnotexist:v1.0",
		host+"/production/doesnotexist:v1.0",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "copying image")
}

func TestCraneProviderCopyIdempotent(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)

	srcRef := host + "/staging/myimage:v1.0"
	digest := pushRandomImage(t, srcRef)

	p := newInsecureCraneProvider()
	dstRef := host + "/production/myimage:v1.0"

	// Copy twice — the second copy should also succeed.
	for i := range 2 {
		err := p.CopyImage(context.Background(), srcRef, dstRef)
		require.NoError(t, err, "copy attempt %d", i+1)
	}

	gotDigest, err := crane.Digest(dstRef, crane.Insecure)
	require.NoError(t, err)
	require.Equal(t, digest, gotDigest)
}

func TestCraneProviderCopyPreservesContent(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)

	// Push a random image and record its layers.
	srcImg, err := random.Image(1024, 2)
	require.NoError(t, err)

	srcRef := host + "/staging/myimage:v1.0"
	err = crane.Push(srcImg, srcRef, crane.Insecure)
	require.NoError(t, err)

	srcLayers, err := srcImg.Layers()
	require.NoError(t, err)

	// Copy with CraneProvider.
	p := newInsecureCraneProvider()
	dstRef := host + "/production/myimage:v1.0"
	err = p.CopyImage(context.Background(), srcRef, dstRef)
	require.NoError(t, err)

	// Pull the destination image and verify layer digests match.
	dstImg, err := crane.Pull(dstRef, crane.Insecure)
	require.NoError(t, err)

	dstLayers, err := dstImg.Layers()
	require.NoError(t, err)
	require.Len(t, dstLayers, len(srcLayers), "layer count mismatch")

	for i := range srcLayers {
		srcDg, err := srcLayers[i].Digest()
		require.NoError(t, err)

		dstDg, err := dstLayers[i].Digest()
		require.NoError(t, err)

		require.Equal(t, srcDg, dstDg, "layer %d digest mismatch", i)
	}

	// Verify config digest matches.
	srcCfgHash, err := srcImg.ConfigName()
	require.NoError(t, err)

	dstCfgHash, err := dstImg.ConfigName()
	require.NoError(t, err)

	require.Equal(t, srcCfgHash, dstCfgHash)
}

func TestCraneProviderWithTransport(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)

	srcRef := host + "/staging/myimage:v1.0"
	srcDigest := pushRandomImage(t, srcRef)

	// Use a counting transport to verify it is actually used.
	counter := &countingTransport{base: http.DefaultTransport}
	p := NewCraneProvider(
		WithTransport(counter),
		WithCraneOptions(crane.Insecure),
	)

	dstRef := host + "/production/myimage:v1.0"
	err := p.CopyImage(context.Background(), srcRef, dstRef)
	require.NoError(t, err)

	require.Positive(t, counter.count, "custom transport was not used")

	gotDigest, err := crane.Digest(dstRef, crane.Insecure)
	require.NoError(t, err)
	require.Equal(t, srcDigest, gotDigest)
}

func TestCraneProviderCopyNestedImagePath(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)

	// Test with a nested sub-path image (e.g., "staging/kubernetes/apiserver").
	srcRef := host + "/staging/kubernetes/apiserver:v1.30.0"
	digest := pushRandomImage(t, srcRef)

	p := newInsecureCraneProvider()
	dstRef := host + "/production/kubernetes/apiserver:v1.30.0"
	err := p.CopyImage(context.Background(), srcRef, dstRef)
	require.NoError(t, err)

	gotDigest, err := crane.Digest(dstRef, crane.Insecure)
	require.NoError(t, err)
	require.Equal(t, digest, gotDigest)
}

func TestCraneProviderPromoteImages(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)
	srcRegistry := image.Registry(host + "/staging")
	dstRegistry := image.Registry(host + "/production")

	type imageEntry struct {
		name   image.Name
		tag    image.Tag
		digest string // filled after push
	}

	entries := []imageEntry{
		{name: "app", tag: "v1.0"},
		{name: "app", tag: "v2.0"},
		{name: "web", tag: "latest"},
	}

	// Push all test images to the staging registry.
	for i, e := range entries {
		ref := fmt.Sprintf("%s/%s:%s", srcRegistry, e.name, e.tag)
		entries[i].digest = pushRandomImage(t, ref)
	}

	// Simulate the promotion flow: for each image, copy from staging
	// to production using FQIN (source by digest) and PQIN (destination by tag),
	// exactly as PromoteImages does.
	p := newInsecureCraneProvider()

	for _, e := range entries {
		srcVertex := fmt.Sprintf("%s/%s@%s", srcRegistry, e.name, e.digest)
		dstVertex := fmt.Sprintf("%s/%s:%s", dstRegistry, e.name, e.tag)

		err := p.CopyImage(context.Background(), srcVertex, dstVertex)
		require.NoError(t, err, "promoting %s to %s", srcVertex, dstVertex)
	}

	// Verify all images landed in production with correct tags and digests.
	for _, e := range entries {
		dstRef := fmt.Sprintf("%s/%s:%s", dstRegistry, e.name, e.tag)
		gotDigest, err := crane.Digest(dstRef, crane.Insecure)
		require.NoError(t, err, "verifying %s", dstRef)
		require.Equal(t, e.digest, gotDigest, "digest mismatch for %s", dstRef)
	}

	// Verify tag listing for a multi-tag image.
	tags, err := crane.ListTags(fmt.Sprintf("%s/app", dstRegistry), crane.Insecure)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"v1.0", "v2.0"}, tags)
}

func TestCraneProviderCopyImageIndex(t *testing.T) {
	t.Parallel()

	host := newTestRegistry(t)

	// Create a random image index (multi-arch manifest list).
	idx, err := random.Index(1024, 1, 2)
	require.NoError(t, err)

	srcRef := host + "/staging/multiarch:v1.0"
	ref, err := name.ParseReference(srcRef, name.Insecure)
	require.NoError(t, err)
	err = remote.WriteIndex(ref, idx, remote.WithTransport(http.DefaultTransport))
	require.NoError(t, err)

	srcDigest, err := crane.Digest(srcRef, crane.Insecure)
	require.NoError(t, err)

	p := newInsecureCraneProvider()
	dstRef := host + "/production/multiarch:v1.0"
	err = p.CopyImage(context.Background(), srcRef, dstRef)
	require.NoError(t, err)

	dstDigest, err := crane.Digest(dstRef, crane.Insecure)
	require.NoError(t, err)
	require.Equal(t, srcDigest, dstDigest)
}

// countingTransport wraps an http.RoundTripper and counts requests.
type countingTransport struct {
	base  http.RoundTripper
	count int
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.count++

	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("round trip: %w", err)
	}

	return resp, nil
}
