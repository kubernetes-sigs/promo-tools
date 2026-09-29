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

package provenance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testSigner  = "sigstore::https://accounts.google.com::sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com"
	testBuilder = "https://prow.k8s.io/job-history/gs/kubernetes-ci-logs/logs/post-security-profiles-operator-push-image"
	testSource  = "github.com/kubernetes-sigs/security-profiles-operator"
)

// testPolicy returns a valid policy in the given mode.
func testPolicy(mode PolicyMode) *Policy {
	return &Policy{
		Mode:     mode,
		Signers:  []string{testSigner},
		Builders: []string{testBuilder},
		Sources:  []string{testSource},
	}
}

func TestPolicyValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		policy  *Policy
		wantErr string
	}{
		{
			name: "nil policy",
		},
		{
			name:   "empty policy is off",
			policy: &Policy{},
		},
		{
			name:   "off policy needs no expectations",
			policy: &Policy{Mode: PolicyModeOff},
		},
		{
			name:   "warn",
			policy: testPolicy(PolicyModeWarn),
		},
		{
			name:   "require",
			policy: testPolicy(PolicyModeRequire),
		},
		{
			name: "regexp signer and all fields",
			policy: &Policy{
				Mode:           PolicyModeRequire,
				Signers:        []string{"sigstore(identityMatch=regex)::https://accounts.google.com::.*@k8s-staging-images\\.iam\\.gserviceaccount\\.com"},
				Builders:       []string{testBuilder},
				Sources:        []string{testSource},
				PredicateTypes: []string{"https://spdx.dev/Document"},
				Level:          2,
			},
		},
		{
			name:    "unknown mode",
			policy:  &Policy{Mode: "enforce"},
			wantErr: `mode must be "off", "warn" or "require", got "enforce"`,
		},
		{
			name: "invalid signer",
			policy: &Policy{
				Mode:     PolicyModeWarn,
				Signers:  []string{"not-a-spec"},
				Builders: []string{testBuilder},
				Sources:  []string{testSource},
			},
			wantErr: `invalid signer "not-a-spec"`,
		},
		{
			name: "signer without identity",
			policy: &Policy{
				Mode:     PolicyModeWarn,
				Signers:  []string{"sigstore(issuerMatch=exact)::https://accounts.google.com::"},
				Builders: []string{testBuilder},
				Sources:  []string{testSource},
			},
			wantErr: "the identity must not be empty",
		},
		{
			name: "key signer",
			policy: &Policy{
				Mode:     PolicyModeWarn,
				Signers:  []string{"key::ecdsa::abc"},
				Builders: []string{testBuilder},
				Sources:  []string{testSource},
			},
			wantErr: "only sigstore signers are supported",
		},
		{
			name:    "require without expectations",
			policy:  &Policy{Mode: PolicyModeRequire},
			wantErr: "at least one signer is required",
		},
		{
			name: "require without builders",
			policy: &Policy{
				Mode:    PolicyModeRequire,
				Signers: []string{testSigner},
				Sources: []string{testSource},
			},
			wantErr: "at least one builder is required",
		},
		{
			name: "require without sources",
			policy: &Policy{
				Mode:     PolicyModeRequire,
				Signers:  []string{testSigner},
				Builders: []string{testBuilder},
			},
			wantErr: "at least one source is required",
		},
		{
			name: "source with ref",
			policy: &Policy{
				Mode:     PolicyModeRequire,
				Signers:  []string{testSigner},
				Builders: []string{testBuilder},
				Sources:  []string{testSource + "@refs/heads/main"},
			},
			wantErr: "must not carry a ref",
		},
		{
			name: "empty builder",
			policy: &Policy{
				Mode:     PolicyModeRequire,
				Signers:  []string{testSigner},
				Builders: []string{" "},
				Sources:  []string{testSource},
			},
			wantErr: "builders must not be empty",
		},
		{
			name: "empty predicate type",
			policy: &Policy{
				Mode:           PolicyModeRequire,
				Signers:        []string{testSigner},
				Builders:       []string{testBuilder},
				Sources:        []string{testSource},
				PredicateTypes: []string{""},
			},
			wantErr: "predicateTypes must not be empty",
		},
		{
			name: "level too high",
			policy: &Policy{
				Mode:     PolicyModeRequire,
				Signers:  []string{testSigner},
				Builders: []string{testBuilder},
				Sources:  []string{testSource},
				Level:    4,
			},
			wantErr: "level must be between 2 and 3, got 4",
		},
		{
			name: "level 1 would not enforce the builders",
			policy: &Policy{
				Mode:     PolicyModeRequire,
				Signers:  []string{testSigner},
				Builders: []string{testBuilder},
				Sources:  []string{testSource},
				Level:    1,
			},
			wantErr: "level must be between 2 and 3, got 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.policy.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestPolicyEnabled(t *testing.T) {
	t.Parallel()

	var nilPolicy *Policy

	require.False(t, nilPolicy.Enabled())
	require.False(t, (&Policy{}).Enabled())
	require.False(t, (&Policy{Mode: PolicyModeOff}).Enabled())
	require.True(t, testPolicy(PolicyModeWarn).Enabled())
	require.True(t, testPolicy(PolicyModeRequire).Enabled())
}

func TestPolicyString(t *testing.T) {
	t.Parallel()

	var nilPolicy *Policy

	require.Equal(t, "off", nilPolicy.String())
	require.Equal(t, "off", (&Policy{Mode: PolicyModeOff, Signers: []string{testSigner}}).String())

	policy := testPolicy(PolicyModeRequire)
	policy.PredicateTypes = []string{"https://spdx.dev/Document"}
	policy.Level = 2

	require.Equal(t,
		"mode=require signers=["+testSigner+"] builders=["+testBuilder+"] sources=["+testSource+"] "+
			"predicateTypes=[https://spdx.dev/Document] level=2",
		policy.String(),
	)
}

func TestPolicyEqual(t *testing.T) {
	t.Parallel()

	var nilPolicy *Policy

	require.True(t, nilPolicy.Equal(nil))
	require.True(t, nilPolicy.Equal(&Policy{Mode: PolicyModeOff}))
	require.True(t, testPolicy(PolicyModeWarn).Equal(testPolicy(PolicyModeWarn)))
	require.False(t, testPolicy(PolicyModeWarn).Equal(testPolicy(PolicyModeRequire)))
	require.False(t, nilPolicy.Equal(testPolicy(PolicyModeWarn)))
}

func TestPolicyEqualOrder(t *testing.T) {
	t.Parallel()

	a := testPolicy(PolicyModeRequire)
	a.Signers = []string{testSigner, "sigstore::https://accounts.google.com::other@example.com"}

	b := testPolicy(PolicyModeRequire)
	b.Signers = []string{"sigstore::https://accounts.google.com::other@example.com", testSigner}

	require.True(t, a.Equal(b), "the order of the lists does not matter")
	require.False(t, a.Equal(testPolicy(PolicyModeRequire)))
	require.False(t, a.Equal(testPolicy(PolicyModeWarn)))
	require.True(t, (*Policy)(nil).Equal(&Policy{Mode: PolicyModeOff}))
	require.False(t, (*Policy)(nil).Equal(a))
}
