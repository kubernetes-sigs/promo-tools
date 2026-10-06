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
	// testGeneratorSigner is the identity of an isolated provenance
	// generator, and testGeneratorBuilder the builder only it can claim.
	// GitHub names the signing workflow in both.
	testGeneratorBuilder = "https://github.com/kubernetes-sigs/security-profiles-operator/.github/workflows/provenance.yml"
	testGeneratorSigner  = "sigstore::https://token.actions.githubusercontent.com::" +
		testGeneratorBuilder + "@refs/tags/v1.2.0"
	testSelfSignedBuilder = "https://example.com/self-signed"

	testSigner  = "sigstore::https://accounts.google.com::sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com"
	testBuilder = "https://prow.k8s.io/job-history/gs/kubernetes-ci-logs/logs/post-security-profiles-operator-push-image"
	testSource  = "github.com/kubernetes-sigs/security-profiles-operator"

	// testLevelImage and testLevelPattern are an image name and a pattern
	// of levels.
	testLevelImage   = "spoc"
	testLevelPattern = "charts/*"
)

// testPolicy returns a valid policy in the given mode.
func testPolicy(mode PolicyMode) *Policy {
	return &Policy{
		Mode:     mode,
		Signers:  []string{testSigner},
		Builders: []Builder{{ID: testBuilder, Level: 3}},
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
				Builders:       []Builder{{ID: testBuilder, Level: 3}},
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
				Builders: []Builder{{ID: testBuilder, Level: 3}},
				Sources:  []string{testSource},
			},
			wantErr: `invalid signer "not-a-spec"`,
		},
		{
			name: "signer without identity",
			policy: &Policy{
				Mode:     PolicyModeWarn,
				Signers:  []string{"sigstore(issuerMatch=exact)::https://accounts.google.com::"},
				Builders: []Builder{{ID: testBuilder, Level: 3}},
				Sources:  []string{testSource},
			},
			wantErr: "the identity must not be empty",
		},
		{
			name: "key signer",
			policy: &Policy{
				Mode:     PolicyModeWarn,
				Signers:  []string{"key::ecdsa::abc"},
				Builders: []Builder{{ID: testBuilder, Level: 3}},
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
				Builders: []Builder{{ID: testBuilder, Level: 3}},
			},
			wantErr: "at least one source is required",
		},
		{
			name: "source with ref",
			policy: &Policy{
				Mode:     PolicyModeRequire,
				Signers:  []string{testSigner},
				Builders: []Builder{{ID: testBuilder, Level: 3}},
				Sources:  []string{testSource + "@refs/heads/main"},
			},
			wantErr: "must not carry a ref",
		},
		{
			name: "empty builder",
			policy: &Policy{
				Mode:     PolicyModeRequire,
				Signers:  []string{testSigner},
				Builders: []Builder{{ID: " ", Level: 3}},
				Sources:  []string{testSource},
			},
			wantErr: "builder IDs must not be empty",
		},
		{
			name: "empty predicate type",
			policy: &Policy{
				Mode:           PolicyModeRequire,
				Signers:        []string{testSigner},
				Builders:       []Builder{{ID: testBuilder, Level: 3}},
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
				Builders: []Builder{{ID: testBuilder, Level: 3}},
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
				Builders: []Builder{{ID: testBuilder, Level: 3}},
				Sources:  []string{testSource},
				Level:    1,
			},
			wantErr: "level must be between 2 and 3, got 1",
		},
		{
			name: "level above the lowest builder",
			policy: &Policy{
				Mode:     PolicyModeRequire,
				Signers:  []string{testSigner},
				Builders: []Builder{{ID: testBuilder, Level: 3}, {ID: "https://example.com/self-signed", Level: 1}},
				Sources:  []string{testSource},
				Level:    2,
			},
			wantErr: "level 2 is above 1, the highest level provenance can verify at with these builders",
		},
		{
			name: "builder without a level",
			policy: &Policy{
				Mode:     PolicyModeRequire,
				Signers:  []string{testSigner},
				Builders: []Builder{{ID: testBuilder}},
				Sources:  []string{testSource},
			},
			wantErr: "needs a level between 1 and 3, got 0",
		},
		{
			name: "builder level too high",
			policy: &Policy{
				Mode:     PolicyModeRequire,
				Signers:  []string{testSigner},
				Builders: []Builder{{ID: testBuilder, Level: 4}},
				Sources:  []string{testSource},
			},
			wantErr: "needs a level between 1 and 3, got 4",
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

// boundBuilderPolicy returns a policy with a self-signed level 1 builder
// the signers no builder names can claim, and a level 3 builder only the
// provenance generator can claim.
func boundBuilderPolicy(level int) *Policy {
	return &Policy{
		Mode:    PolicyModeRequire,
		Signers: []string{testSigner, testGeneratorSigner},
		Builders: []Builder{
			{ID: testSelfSignedBuilder, Level: 1},
			{ID: testGeneratorBuilder, Level: 3, Signers: []string{testGeneratorSigner}},
		},
		Sources: []string{testSource},
		Level:   level,
	}
}

func TestPolicyBuilderSigners(t *testing.T) {
	t.Parallel()

	require.NoError(t, boundBuilderPolicy(3).Validate(), "a builder bound to its signer reaches level 3")

	policy := boundBuilderPolicy(0)

	require.Equal(t, 3, policy.highestLevel())
	require.Equal(t, []Builder{policy.Builders[0]}, policy.claimableBuilders(nil))
	require.Equal(t, []Builder{policy.Builders[0]}, policy.claimableBuilders([]string{testSigner}))
	require.Equal(t, []Builder{policy.Builders[1]}, policy.claimableBuilders([]string{testGeneratorSigner}))
	require.Equal(t, []Builder{policy.Builders[1]}, policy.claimableBuilders([]string{testSigner, testGeneratorSigner}),
		"a signer that builders name can't claim the builders without signers")

	require.Contains(t, policy.String(), testGeneratorBuilder+" (level 3, signers "+testGeneratorSigner+")")

	other := boundBuilderPolicy(0)
	other.Builders[1].Signers = nil
	require.False(t, policy.Equal(other), "the builder signers are part of the policy")
	require.Equal(t, 1, other.highestLevel())

	policy.Builders[1].Signers = []string{testGeneratorSigner, testSigner}
	other.Builders[1].Signers = []string{testSigner, testGeneratorSigner}
	require.True(t, policy.Equal(other), "the order of the builder signers doesn't matter")
	require.Equal(t, policy.String(), other.String())

	require.Zero(t, (&Policy{Builders: policy.Builders}).highestLevel(), "no signers")
}

func TestPolicyValidateBuilderSigners(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		builders []Builder
		wantErr  string
	}{
		{
			name: "signer that isn't a policy signer",
			builders: []Builder{
				{ID: testGeneratorBuilder, Level: 3, Signers: []string{foreignSigner}},
			},
			wantErr: "must be one of the policy signers",
		},
		{
			name: "empty signer",
			builders: []Builder{
				{ID: testGeneratorBuilder, Level: 3, Signers: []string{""}},
			},
			wantErr: "must be one of the policy signers",
		},
		{
			name: "same builder twice",
			builders: []Builder{
				{ID: testSelfSignedBuilder, Level: 1},
				{ID: testSelfSignedBuilder, Level: 1},
			},
			wantErr: "is listed more than once",
		},
		{
			name: "same builder with and without signers",
			builders: []Builder{
				{ID: testGeneratorBuilder, Level: 1},
				{ID: testGeneratorBuilder, Level: 3, Signers: []string{testGeneratorSigner}},
			},
			wantErr: "is listed more than once",
		},
		{
			name: "builder without signers at any ref",
			builders: []Builder{
				{ID: testGeneratorBuilder, Level: 1},
				{ID: testGeneratorBuilder + "@refs/tags/v1.2.0", Level: 3, Signers: []string{testGeneratorSigner}},
			},
			wantErr: "overlap, but only one of them names signers",
		},
		{
			name: "builder with signers at any ref",
			builders: []Builder{
				{ID: testGeneratorBuilder + "@refs/heads/main", Level: 1},
				{ID: testGeneratorBuilder, Level: 3, Signers: []string{testGeneratorSigner}},
			},
			wantErr: "overlap, but only one of them names signers",
		},
		{
			name: "overlapping builders without signers",
			builders: []Builder{
				{ID: testGeneratorBuilder, Level: 1},
				{ID: testGeneratorBuilder + "@refs/heads/main", Level: 1},
			},
		},
		{
			name: "builders that only share a prefix",
			builders: []Builder{
				{ID: testGeneratorBuilder, Level: 1},
				{ID: testGeneratorBuilder + "2", Level: 3, Signers: []string{testGeneratorSigner}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := boundBuilderPolicy(0)
			policy.Builders = tc.builders

			err := policy.Validate()
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
		"mode=require signers=["+testSigner+"] builders=["+testBuilder+" (level 3)] sources=["+testSource+"] "+
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

func TestPolicyValidateLevels(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		levels  []ImageLevel
		wantErr string
	}{
		{
			name:   "images at the levels of the builders",
			levels: []ImageLevel{{Images: []string{testLevelImage, testLevelPattern}, Level: 3}, {Images: []string{"*-bundle"}, Level: 2}},
		},
		{
			// Every satisfied image reaches level 1 anyway.
			name:    "level 1 requires nothing",
			levels:  []ImageLevel{{Images: []string{"*-bundle"}, Level: 1}},
			wantErr: "levels of *-bundle must be between 2 and 3, got 1",
		},
		{
			name:    "image pattern with a leading slash",
			levels:  []ImageLevel{{Images: []string{"/spoc"}, Level: 3}},
			wantErr: `image pattern "/spoc" in levels must not start or end with a /`,
		},
		{
			name:    "image pattern with a trailing slash",
			levels:  []ImageLevel{{Images: []string{"charts/"}, Level: 3}},
			wantErr: `image pattern "charts/" in levels must not start or end with a /`,
		},
		{
			name:    "entry without images",
			levels:  []ImageLevel{{Level: 3}},
			wantErr: "every entry of levels needs at least one image",
		},
		{
			name:    "empty image pattern",
			levels:  []ImageLevel{{Images: []string{" "}, Level: 3}},
			wantErr: "image patterns of levels must not be empty",
		},
		{
			name:    "invalid image pattern",
			levels:  []ImageLevel{{Images: []string{"spoc["}, Level: 3}},
			wantErr: `invalid image pattern "spoc[" in levels`,
		},
		{
			name:    "level without a value",
			levels:  []ImageLevel{{Images: []string{testLevelImage}}},
			wantErr: "levels of spoc must be between 2 and 3, got 0",
		},
		{
			name:    "level too high",
			levels:  []ImageLevel{{Images: []string{testLevelImage}, Level: 4}},
			wantErr: "levels of spoc must be between 2 and 3, got 4",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := boundBuilderPolicy(0)
			policy.Levels = tc.levels

			err := policy.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, tc.wantErr)
		})
	}

	// No signer can claim more than the level 1 builder.
	policy := testPolicy(PolicyModeRequire)
	policy.Builders[0].Level = 1
	policy.Levels = []ImageLevel{{Images: []string{testLevelImage}, Level: 2}}
	require.ErrorContains(t, policy.Validate(),
		"level 2 of spoc is above 1, the highest level provenance can verify at with these builders")
}

func TestPolicyRequiredLevel(t *testing.T) {
	t.Parallel()

	var nilPolicy *Policy

	require.Zero(t, nilPolicy.RequiredLevel(testLevelImage))

	policy := boundBuilderPolicy(0)
	require.Zero(t, policy.RequiredLevel(testLevelImage), "without levels nothing is required")

	policy.Levels = []ImageLevel{
		{Images: []string{testLevelImage, testLevelPattern}, Level: 3},
		{Images: []string{"security-profiles-operator*"}, Level: 2},
		// Overlapping entries: the highest level wins.
		{Images: []string{"security-profiles-operator-amd64"}, Level: 3},
	}

	for image, want := range map[string]int{
		testLevelImage:                      3,
		"/spoc/":                            3,
		"charts/security-profiles-operator": 3,
		"charts/nested/chart":               0,
		"security-profiles-operator":        2,
		"security-profiles-operator-amd64":  3,
		"security-profiles-operator-arm64":  2,
		"spoc-amd64":                        0,
		"":                                  0,
	} {
		require.Equal(t, want, policy.RequiredLevel(image), image)
	}

	// An image at the source registry itself has no name, which even *
	// doesn't match.
	policy.Levels = []ImageLevel{{Images: []string{"*"}, Level: 3}}
	require.Zero(t, policy.RequiredLevel(""))
	require.Zero(t, policy.RequiredLevel("/"))
	require.Equal(t, 3, policy.RequiredLevel(testLevelImage))
}

func TestPolicyLevelsStringAndEqual(t *testing.T) {
	t.Parallel()

	const bundle = "bundle"

	a := testPolicy(PolicyModeRequire)
	a.Levels = []ImageLevel{{Images: []string{testLevelImage, testLevelPattern}, Level: 3}, {Images: []string{bundle}, Level: 2}}

	require.Contains(t, a.String(), "levels=[charts/*, spoc (level 3); bundle (level 2)]")

	b := testPolicy(PolicyModeRequire)
	b.Levels = []ImageLevel{{Images: []string{bundle}, Level: 2}, {Images: []string{testLevelPattern, testLevelImage}, Level: 3}}

	require.True(t, a.Equal(b), "the order of the levels and their images does not matter")
	require.False(t, a.Equal(testPolicy(PolicyModeRequire)))

	// The same requirements, grouped differently.
	c := testPolicy(PolicyModeRequire)
	c.Levels = []ImageLevel{
		{Images: []string{testLevelImage}, Level: 3},
		{Images: []string{testLevelPattern}, Level: 3},
		{Images: []string{bundle, testLevelImage}, Level: 2},
	}
	require.True(t, a.Equal(c), "only the highest level per pattern counts")

	b.Levels[0].Level = 3
	require.False(t, a.Equal(b))
}

func TestPolicyUnmatchedLevelPatterns(t *testing.T) {
	t.Parallel()

	// An absolute image name never matches.
	const absolute = "registry.k8s.io/spoc"

	var nilPolicy *Policy

	require.Empty(t, nilPolicy.UnmatchedLevelPatterns([]string{testLevelImage}))

	policy := boundBuilderPolicy(0)
	policy.Levels = []ImageLevel{
		{Images: []string{testLevelImage, testLevelPattern}, Level: 3},
		{Images: []string{absolute, testLevelImage}, Level: 2},
	}

	require.Equal(t, []string{absolute},
		policy.UnmatchedLevelPatterns([]string{testLevelImage, "charts/security-profiles-operator"}))
	require.Equal(t, []string{testLevelImage, testLevelPattern, absolute},
		policy.UnmatchedLevelPatterns(nil))
}
