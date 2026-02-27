/*
Copyright 2022 The Kubernetes Authors.

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
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/release-utils/env"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// TestGetIdentityToken tests the identity token generation logic. By default
// it will test using the testSigningAccount defined in sign.go. For local testing
// purposes you can override the target account with another one setting
// TEST_SERVICE_ACCOUNT and accessing it with an identity set in a credentials
// file in CIP_E2E_KEY_FILE.
func TestGetIdentityToken(t *testing.T) {
	// This unit needs a valid credentials to run
	if os.Getenv("CIP_E2E_KEY_FILE") == "" {
		return
	}

	opts := &options.Options{
		SignerInitCredentials: os.Getenv("CIP_E2E_KEY_FILE"),
	}

	di := DefaultPromoterImplementation{}
	_, err := di.GetIdentityToken(opts, "fakeAccount@iam.project..")
	require.Error(t, err)

	tok, err := di.GetIdentityToken(
		opts, env.Default("TEST_SERVICE_ACCOUNT", TestSigningAccount),
	)

	require.NoError(t, err)
	require.NotEmpty(t, tok)
}

func TestDigestToSignatureTag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		digest image.Digest
		want   string
	}{
		{
			name:   "standard sha256 digest",
			digest: "sha256:709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f",
			want:   "sha256-709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f.sig",
		},
		{
			name:   "bare sha256 prefix",
			digest: "sha256:abc",
			want:   "sha256-abc.sig",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, digestToSignatureTag(tc.digest))
		})
	}
}

func TestDigestToSBOMTag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		digest image.Digest
		want   string
	}{
		{
			name:   "standard sha256 digest",
			digest: "sha256:709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f",
			want:   "sha256-709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f.sbom",
		},
		{
			name:   "bare sha256 prefix",
			digest: "sha256:abc",
			want:   "sha256-abc.sbom",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, digestToSBOMTag(tc.digest))
		})
	}
}

func TestIsTransient(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "429 Too Many Requests",
			err:  &transport.Error{StatusCode: http.StatusTooManyRequests},
			want: true,
		},
		{
			name: "500 Internal Server Error",
			err:  &transport.Error{StatusCode: http.StatusInternalServerError},
			want: true,
		},
		{
			name: "502 Bad Gateway",
			err:  &transport.Error{StatusCode: http.StatusBadGateway},
			want: true,
		},
		{
			name: "503 Service Unavailable",
			err:  &transport.Error{StatusCode: http.StatusServiceUnavailable},
			want: true,
		},
		{
			name: "404 Not Found",
			err:  &transport.Error{StatusCode: http.StatusNotFound},
			want: false,
		},
		{
			name: "401 Unauthorized",
			err:  &transport.Error{StatusCode: http.StatusUnauthorized},
			want: false,
		},
		{
			name: "403 Forbidden",
			err:  &transport.Error{StatusCode: http.StatusForbidden},
			want: false,
		},
		{
			name: "non-transport error",
			err:  errors.New("network timeout"),
			want: false,
		},
		{
			name: "wrapped transport 429",
			err:  fmt.Errorf("copy failed: %w", &transport.Error{StatusCode: http.StatusTooManyRequests}),
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ratelimit.IsTransient(tc.err))
		})
	}
}

func TestTargetIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		edge   *promotion.Edge
		assert func(string)
	}{
		{
			name: "modified reference with real-world example",
			edge: &promotion.Edge{
				DstRegistry: registry.Context{Name: "us-west2-docker.pkg.dev/k8s-artifacts-prod/images/kubernetes"},
				DstImageTag: promotion.ImageTag{Name: "conformance-arm64"},
				Digest:      "sha256:709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f",
			},
			assert: func(res string) {
				require.Equal(t, "registry.k8s.io/kubernetes/conformance-arm64", res)
			},
		},
		{
			name: "modified reference with simple example",
			edge: &promotion.Edge{
				DstRegistry: registry.Context{Name: "registry/k8s-artifacts-prod/images"},
				DstImageTag: promotion.ImageTag{Name: "image"},
				Digest:      "sha256",
			},
			assert: func(res string) {
				require.Equal(t, "registry.k8s.io/image", res)
			},
		},
		{
			name: "not modified reference",
			edge: &promotion.Edge{
				DstRegistry: registry.Context{Name: "foo-bar"},
				DstImageTag: promotion.ImageTag{Name: "conformance-arm64"},
				Digest:      "sha256:709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f",
			},
			assert: func(res string) {
				require.Equal(t, "foo-bar/conformance-arm64", res)
			},
		},
	} {
		edge := tc.edge
		assert := tc.assert

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := targetIdentity(edge)
			assert(res)
		})
	}
}

func TestGroupEdgesByIdentityDigest(t *testing.T) {
	t.Parallel()

	digest1 := image.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	digest2 := image.Digest("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	// Use production-style registry names so targetIdentity normalizes them
	// to the same identity (registry.k8s.io/app), allowing edges with
	// different registries to be grouped together.
	mkEdge := func(dstReg string, imgName string, digest image.Digest, tag image.Tag) promotion.Edge {
		return promotion.Edge{
			SrcRegistry: registry.Context{Name: "staging", Src: true},
			SrcImageTag: promotion.ImageTag{Name: image.Name(imgName), Tag: tag},
			Digest:      digest,
			DstRegistry: registry.Context{Name: image.Registry(dstReg)},
			DstImageTag: promotion.ImageTag{Name: image.Name(imgName), Tag: tag},
		}
	}

	edges := map[promotion.Edge]any{
		// Same normalized identity + digest, different registries → same group.
		mkEdge("us-central1-docker.pkg.dev/k8s-artifacts-prod/images", "app", digest1, "v1.0"): nil,
		mkEdge("us-east1-docker.pkg.dev/k8s-artifacts-prod/images", "app", digest1, "v1.0"):    nil,
		mkEdge("us-west1-docker.pkg.dev/k8s-artifacts-prod/images", "app", digest1, "v1.0"):    nil,
		// Different digest → different group.
		mkEdge("us-central1-docker.pkg.dev/k8s-artifacts-prod/images", "app", digest2, "v2.0"): nil,
		// Metadata layers should be skipped.
		mkEdge("us-central1-docker.pkg.dev/k8s-artifacts-prod/images", "app", digest1, "sha256-aaa.sig"): nil,
		mkEdge("us-central1-docker.pkg.dev/k8s-artifacts-prod/images", "app", digest1, "sha256-aaa.att"): nil,
		// Tagless edge should be skipped.
		mkEdge("us-central1-docker.pkg.dev/k8s-artifacts-prod/images", "app", digest1, ""): nil,
	}

	groups := groupEdgesByIdentityDigest(edges)

	// Should have 2 groups (digest1 and digest2), metadata/tagless skipped.
	require.Len(t, groups, 2)

	// Sort groups by digest for deterministic assertion.
	sort.Slice(groups, func(i, j int) bool {
		return groups[i][0].Digest < groups[j][0].Digest
	})

	// Group 1: digest1 with 3 edges (us-central1, us-east1, us-west1).
	require.Len(t, groups[0], 3)
	// Edges should be sorted by destination registry name.
	require.Equal(t, image.Registry("us-central1-docker.pkg.dev/k8s-artifacts-prod/images"), groups[0][0].DstRegistry.Name)
	require.Equal(t, image.Registry("us-east1-docker.pkg.dev/k8s-artifacts-prod/images"), groups[0][1].DstRegistry.Name)
	require.Equal(t, image.Registry("us-west1-docker.pkg.dev/k8s-artifacts-prod/images"), groups[0][2].DstRegistry.Name)

	// Group 2: digest2 with 1 edge.
	require.Len(t, groups[1], 1)
	require.Equal(t, digest2, groups[1][0].Digest)
}
