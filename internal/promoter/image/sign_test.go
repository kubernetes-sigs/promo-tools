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
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/release-utils/env"

	reg "sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry/registry"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
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

func TestTargetIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		edge   *reg.PromotionEdge
		assert func(string)
	}{
		{
			name: "modified reference with real-world example",
			edge: &reg.PromotionEdge{
				DstRegistry: registry.Context{Name: "us-west2-docker.pkg.dev/k8s-artifacts-prod/images/kubernetes"},
				DstImageTag: reg.ImageTag{Name: "conformance-arm64"},
				Digest:      "sha256:709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f",
			},
			assert: func(res string) {
				require.Equal(t, "registry.k8s.io/kubernetes/conformance-arm64", res)
			},
		},
		{
			name: "modified reference with simple example",
			edge: &reg.PromotionEdge{
				DstRegistry: registry.Context{Name: "registry/k8s-artifacts-prod/images"},
				DstImageTag: reg.ImageTag{Name: "image"},
				Digest:      "sha256",
			},
			assert: func(res string) {
				require.Equal(t, "registry.k8s.io/image", res)
			},
		},
		{
			name: "not modified reference",
			edge: &reg.PromotionEdge{
				DstRegistry: registry.Context{Name: "foo-bar"},
				DstImageTag: reg.ImageTag{Name: "conformance-arm64"},
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
