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
	options "sigs.k8s.io/promo-tools/v3/promoter/image/options"
	"sigs.k8s.io/release-utils/env"
)

// TestGetIdentityToken tests the identity token generation logic. By default
// it will test using the testSigningAccount defined in sign.go. For local testing
// purposes you can override the target account with another one setting
// TEST_SERVICE_ACCOUNT and accessing it with an identity set in a credentials
// file in CIP_E2E_KEY_FILE
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
