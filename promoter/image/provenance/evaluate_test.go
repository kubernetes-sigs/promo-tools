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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/policylabs/collector/envelope/bare"
	"github.com/policylabs/collector/envelope/bundle"
	sapi "github.com/policylabs/signer/api/v1"
	"github.com/stretchr/testify/require"
)

const (
	testDigest       = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	otherDigest      = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	testRef          = "us-central1-docker.pkg.dev/k8s-staging-images/sp-operator/security-profiles-operator@" + testDigest
	foreignSigner    = "sigstore::https://accounts.google.com::someone-else@example.iam.gserviceaccount.com"
	spdxType         = "https://spdx.dev/Document"
	slsaV1Type       = "https://slsa.dev/provenance/v1"
	testBuildType    = "https://cloudbuild.googleapis.com/CloudBuildYaml@v1"
	testInvocation   = "https://prow.k8s.io/view/gs/kubernetes-ci-logs/logs/post-security-profiles-operator-push-image/1"
	testSourceURI    = "git+https://" + testSource + "@refs/heads/main"
	otherSourceRepo  = "github.com/kubernetes-sigs/other"
	untrustedBuilder = "https://prow.k8s.io/untrusted"
)

// subjectsFor returns the in-toto subjects naming the digest.
func subjectsFor(digest string) []inTotoSubject {
	algo, hex, _ := strings.Cut(digest, ":")

	return []inTotoSubject{
		{Name: "security-profiles-operator", Digest: map[string]string{algo: hex}},
	}
}

// provenanceOptions tunes the build provenance a test creates.
type provenanceOptions struct {
	digest       string
	builder      string
	source       string
	noInvocation bool

	// workflow is the calling workflow GitHub records in the external
	// parameters.
	workflow *workflowRun
}

// provenanceStatement returns a SLSA v1 build provenance statement.
func provenanceStatement(t *testing.T, opts provenanceOptions) []byte {
	t.Helper()

	if opts.digest == "" {
		opts.digest = testDigest
	}

	if opts.builder == "" {
		opts.builder = testBuilder
	}

	if opts.source == "" {
		opts.source = testSourceURI
	}

	metadata := map[string]any{}
	if !opts.noInvocation {
		metadata["invocationId"] = testInvocation
	}

	externalParameters := map[string]any{}
	if opts.workflow != nil {
		externalParameters["workflow"] = opts.workflow
	}

	data, err := json.Marshal(inTotoStatement{
		Type:          inTotoStatementType,
		PredicateType: slsaV1Type,
		Subject:       subjectsFor(opts.digest),
		Predicate: map[string]any{
			"buildDefinition": map[string]any{
				"buildType":          testBuildType,
				"externalParameters": externalParameters,
				"resolvedDependencies": []map[string]any{
					{uriField: opts.source, digestField: map[string]string{"gitCommit": strings.Repeat("a", 40)}},
				},
			},
			"runDetails": map[string]any{
				"builder":  map[string]any{"id": opts.builder},
				"metadata": metadata,
			},
		},
	})
	require.NoError(t, err)

	return data
}

// sbomStatement returns an SBOM statement with a JSON predicate.
func sbomStatement(t *testing.T, digest string) []byte {
	t.Helper()

	data, err := json.Marshal(inTotoStatement{
		Type:          inTotoStatementType,
		PredicateType: spdxType,
		Subject:       subjectsFor(digest),
		Predicate:     map[string]any{"spdxVersion": "SPDX-2.3"},
	})
	require.NoError(t, err)

	return data
}

// signed records a verified signature by the signer on the statement.
func signed(t *testing.T, principal string) *sapi.Verification {
	t.Helper()

	id, err := sapi.NewIdentityFromPrincipal(principal)
	require.NoError(t, err)

	return &sapi.Verification{Signature: &sapi.SignatureVerification{
		Verified:   true,
		Status:     sapi.VerificationStatus_VERIFIED,
		Identities: []*sapi.Identity{id},
	}}
}

// refuted records a signature that did not verify.
func refuted(t *testing.T, principal string) *sapi.Verification {
	t.Helper()

	v := signed(t, principal)
	v.Signature.Verified = false
	v.Signature.Status = sapi.VerificationStatus_FAILED
	v.Signature.Error = "signature mismatch"

	return v
}

// newAttestation parses the statement and records the verification.
func newAttestation(t *testing.T, data []byte, verification *sapi.Verification) Attestation {
	t.Helper()

	envs, err := bare.New().ParseStream(bytes.NewReader(data))
	require.NoError(t, err)
	require.Len(t, envs, 1)

	env := envs[0]
	statement := env.GetStatement()
	require.NotNil(t, statement)

	status := SignatureUnsigned

	if verification != nil {
		statement.GetPredicate().SetVerification(verification)

		status = SignatureFailed
		if verification.GetVerified() {
			status = SignatureVerified
		}
	}

	return Attestation{
		Digest:        testDigest,
		Source:        SourceReferrer,
		Location:      "sha256:" + strings.Repeat("f", 64),
		PredicateType: string(statement.GetPredicateType()),
		Status:        status,
		Envelope:      env,
	}
}

func newDiscovery(atts ...Attestation) *Discovery {
	return &Discovery{Reference: testRef, Attestations: atts}
}

const (
	// controlBuilderTrusted is the SLSA verifier control that checks the
	// builder.
	controlBuilderTrusted = "builder-id-trusted"

	// testIsolatedBuilder is a builder the SLSA verifier doesn't know.
	testIsolatedBuilder = "https://example.com/isolated"
)

// evaluateTestCase is a policy evaluation test case.
type evaluateTestCase struct {
	name       string
	policy     func() *Policy
	discovery  func(t *testing.T) *Discovery
	satisfied  bool
	level      int
	provenance string
	violations []string
}

// goodProvenance returns build provenance that satisfies testPolicy.
func goodProvenance(t *testing.T) Attestation {
	t.Helper()

	return newAttestation(t, provenanceStatement(t, provenanceOptions{}), signed(t, testSigner))
}

// claimedBy returns a discovery with provenance of the builder, signed by
// the signer.
func claimedBy(builder, signer string) func(t *testing.T) *Discovery {
	return func(t *testing.T) *Discovery {
		t.Helper()

		return newDiscovery(newAttestation(t,
			provenanceStatement(t, provenanceOptions{builder: builder}), signed(t, signer)))
	}
}

// bothLocations are the locations of the provenances of claimedByBoth, in
// order.
var bothLocations = [2]string{"sha256:" + strings.Repeat("1", 64), "sha256:" + strings.Repeat("2", 64)}

// claimedByBoth returns a discovery with provenance of both builders at
// distinct locations, each signed by testGeneratorSigner when the builder
// is the generator and by testSigner otherwise.
func claimedByBoth(first, second string) func(t *testing.T) *Discovery {
	return func(t *testing.T) *Discovery {
		t.Helper()

		builders := []string{first, second}
		atts := make([]Attestation, 0, len(builders))

		for i, builder := range builders {
			signer := testSigner
			if strings.HasPrefix(builder, testGeneratorBuilder) {
				signer = testGeneratorSigner
			}

			att := newAttestation(t, provenanceStatement(t, provenanceOptions{builder: builder}), signed(t, signer))
			att.Location = bothLocations[i]
			atts = append(atts, att)
		}

		return newDiscovery(atts...)
	}
}

// isolatedBuilderPolicy returns a policy with a level 3 builder that only
// foreignSigner can claim, and that the SLSA verifier doesn't know, so
// that it would bind the builder to any expected signer. With
// selfSigned, testSigner can claim a self-signed level 1 builder.
func isolatedBuilderPolicy(selfSigned bool) *Policy {
	p := &Policy{
		Mode:     PolicyModeRequire,
		Signers:  []string{testSigner, foreignSigner},
		Builders: []Builder{{ID: testIsolatedBuilder, Level: 3, Signers: []string{foreignSigner}}},
		Sources:  []string{testSource},
	}

	if selfSigned {
		p.Builders = append(p.Builders, Builder{ID: testSelfSignedBuilder, Level: 1})
	}

	return p
}

// overlappingSignersPolicy returns a policy whose generator signer also
// matches a regexp signer of the level 2 build workflow.
func overlappingSignersPolicy(level int) *Policy {
	const (
		buildBuilder = "https://github.com/kubernetes-sigs/security-profiles-operator/.github/workflows/build.yml"
		workflows    = "sigstore(identityMatch=regex)::https://token.actions.githubusercontent.com::" +
			`https://github\.com/kubernetes-sigs/security-profiles-operator/\.github/workflows/.*`
	)

	return &Policy{
		Mode:    PolicyModeRequire,
		Signers: []string{testGeneratorSigner, workflows},
		Builders: []Builder{
			{ID: testGeneratorBuilder, Level: 3, Signers: []string{testGeneratorSigner}},
			{ID: buildBuilder, Level: 2, Signers: []string{workflows}},
		},
		Sources: []string{testSource},
		Level:   level,
	}
}

// builderSignerTestCases cover builders that name their signers.
func builderSignerTestCases() []evaluateTestCase {
	generatorRelease := testGeneratorBuilder + "@refs/tags/v1.2.0"

	return []evaluateTestCase{
		{
			// The level 3 provenance counts whichever comes first.
			name:       "the highest level of several passing provenances counts",
			policy:     func() *Policy { return boundBuilderPolicy(0) },
			discovery:  claimedByBoth(testSelfSignedBuilder, generatorRelease),
			satisfied:  true,
			level:      3,
			provenance: bothLocations[1],
		},
		{
			name:       "the highest level counts in any order",
			policy:     func() *Policy { return boundBuilderPolicy(0) },
			discovery:  claimedByBoth(generatorRelease, testSelfSignedBuilder),
			satisfied:  true,
			level:      3,
			provenance: bothLocations[0],
		},
		{
			name:       "the first provenance counts on a tie",
			policy:     func() *Policy { return boundBuilderPolicy(0) },
			discovery:  claimedByBoth(testSelfSignedBuilder, testSelfSignedBuilder),
			satisfied:  true,
			level:      1,
			provenance: bothLocations[0],
		},
		{
			// Only the provenance generator can claim the level 3 builder.
			name:      "a builder bound to the signer verifies at its level",
			policy:    func() *Policy { return boundBuilderPolicy(0) },
			discovery: claimedBy(generatorRelease, testGeneratorSigner),
			satisfied: true,
			level:     3,
		},
		{
			name:      "a builder bound to the signer satisfies the policy level",
			policy:    func() *Policy { return boundBuilderPolicy(3) },
			discovery: claimedBy(generatorRelease, testGeneratorSigner),
			satisfied: true,
			level:     3,
		},
		{
			name:       "other signers can't claim a bound builder",
			policy:     func() *Policy { return boundBuilderPolicy(0) },
			discovery:  claimedBy(generatorRelease, testSigner),
			violations: []string{controlBuilderTrusted},
		},
		{
			name:       "other signers stay below the policy level",
			policy:     func() *Policy { return boundBuilderPolicy(3) },
			discovery:  claimedBy(testSelfSignedBuilder, testSigner),
			violations: []string{"reach SLSA build level 1, the policy requires 3"},
		},
		{
			name:      "a builder unknown to the verifier bound to the signer verifies at its level",
			policy:    func() *Policy { return isolatedBuilderPolicy(true) },
			discovery: claimedBy(testIsolatedBuilder, foreignSigner),
			satisfied: true,
			level:     3,
		},
		{
			// The verifier binds an unknown builder to any expected signer.
			name:       "other signers can't claim a bound builder unknown to the verifier",
			policy:     func() *Policy { return isolatedBuilderPolicy(true) },
			discovery:  claimedBy(testIsolatedBuilder, testSigner),
			violations: []string{controlBuilderTrusted},
		},
		{
			name:       "a signer that builders name can't claim the builders without signers",
			policy:     func() *Policy { return isolatedBuilderPolicy(true) },
			discovery:  claimedBy(testSelfSignedBuilder, foreignSigner),
			violations: []string{controlBuilderTrusted},
		},
		{
			name:       "a signer no builder names can't claim builders that name signers",
			policy:     func() *Policy { return isolatedBuilderPolicy(false) },
			discovery:  claimedBy(testIsolatedBuilder, testSigner),
			violations: []string{"not signed by a signer the builders name"},
		},
		{
			// The signer could claim the build workflow, too.
			name:      "a signer that matches several signers verifies at the lowest level of their builders",
			policy:    func() *Policy { return overlappingSignersPolicy(0) },
			discovery: claimedBy(generatorRelease, testGeneratorSigner),
			satisfied: true,
			level:     2,
		},
		{
			name:       "a signer that matches several signers stays below the policy level",
			policy:     func() *Policy { return overlappingSignersPolicy(3) },
			discovery:  claimedBy(generatorRelease, testGeneratorSigner),
			violations: []string{"reach SLSA build level 2, the policy requires 3"},
		},
	}
}

// provenanceTestCases cover the build provenance checks.
func provenanceTestCases() []evaluateTestCase {
	return []evaluateTestCase{
		{
			name: "signed provenance satisfies the policy",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(goodProvenance(t))
			},
			satisfied: true,
			level:     3,
		},
		{
			name: "no attestations",
			discovery: func(*testing.T) *Discovery {
				return newDiscovery()
			},
			violations: []string{"no SLSA build provenance found"},
		},
		{
			name: "unsigned provenance",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(newAttestation(t, provenanceStatement(t, provenanceOptions{}), nil))
			},
			violations: []string{"not signed or signature did not verify"},
		},
		{
			name: "refuted signature",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(newAttestation(t, provenanceStatement(t, provenanceOptions{}), refuted(t, testSigner)))
			},
			violations: []string{"signature did not verify: signature mismatch"},
		},
		{
			name: "foreign signer",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(newAttestation(t, provenanceStatement(t, provenanceOptions{}), signed(t, foreignSigner)))
			},
			violations: []string{"does not match any expected"},
		},
		{
			name: "provenance about another digest",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(newAttestation(t,
					provenanceStatement(t, provenanceOptions{digest: otherDigest}), signed(t, testSigner)))
			},
			violations: []string{"not about sha256:" + strings.Repeat("1", 64)},
		},
		{
			name: "untrusted builder",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(newAttestation(t,
					provenanceStatement(t, provenanceOptions{builder: "https://example.com/builder"}), signed(t, testSigner)))
			},
			violations: []string{controlBuilderTrusted},
		},
		{
			name: "other source",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(newAttestation(t,
					provenanceStatement(t, provenanceOptions{source: "git+https://" + otherSourceRepo}), signed(t, testSigner)))
			},
			violations: []string{"source-repo-match"},
		},
		{
			name: "second source matches",
			policy: func() *Policy {
				p := testPolicy(PolicyModeRequire)
				p.Sources = []string{otherSourceRepo, testSource}

				return p
			},
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(goodProvenance(t))
			},
			satisfied: true,
			level:     3,
		},
		{
			name: "one good provenance among bad ones",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(
					newAttestation(t, provenanceStatement(t, provenanceOptions{}), signed(t, foreignSigner)),
					goodProvenance(t),
				)
			},
			satisfied: true,
			level:     3,
		},
		{
			name: "regexp signer",
			policy: func() *Policy {
				p := testPolicy(PolicyModeRequire)
				p.Signers = []string{"sigstore(identityMatch=regex)::https://accounts.google.com::.*@k8s-staging-images\\.iam\\.gserviceaccount\\.com"}

				return p
			},
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(goodProvenance(t))
			},
			satisfied: true,
			level:     3,
		},
		{
			name: "missing invocation fails without a level",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(newAttestation(t,
					provenanceStatement(t, provenanceOptions{noInvocation: true}), signed(t, testSigner)))
			},
			violations: []string{"provenance-has-invocation-id"},
		},
		{
			// The provenance can't show how isolated its builder is.
			name: "the builder level caps the verified level",
			policy: func() *Policy {
				p := testPolicy(PolicyModeRequire)
				p.Builders[0].Level = 1

				return p
			},
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(newAttestation(t, provenanceStatement(t, provenanceOptions{}), signed(t, testSigner)))
			},
			satisfied: true,
			level:     1,
		},
		{
			// Any trusted signer can claim the level 3 builder.
			name: "the lowest builder level caps the verified level",
			policy: func() *Policy {
				p := testPolicy(PolicyModeRequire)
				p.Builders = append(p.Builders, Builder{ID: testSelfSignedBuilder, Level: 1})

				return p
			},
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(newAttestation(t, provenanceStatement(t, provenanceOptions{}), signed(t, testSigner)))
			},
			satisfied: true,
			level:     1,
		},
		{
			name: "missing invocation passes at level 2",
			policy: func() *Policy {
				p := testPolicy(PolicyModeRequire)
				p.Level = 2

				return p
			},
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(newAttestation(t,
					provenanceStatement(t, provenanceOptions{noInvocation: true}), signed(t, testSigner)))
			},
			satisfied: true,
			level:     2,
		},
	}
}

// predicateTypeTestCases cover the required predicate types.
func predicateTypeTestCases() []evaluateTestCase {
	return []evaluateTestCase{
		{
			name: "required predicate type present",
			policy: func() *Policy {
				p := testPolicy(PolicyModeRequire)
				p.PredicateTypes = []string{spdxType}

				return p
			},
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(
					goodProvenance(t),
					newAttestation(t, sbomStatement(t, testDigest), signed(t, testSigner)),
				)
			},
			satisfied: true,
			level:     3,
		},
		{
			name: "required predicate type missing",
			policy: func() *Policy {
				p := testPolicy(PolicyModeRequire)
				p.PredicateTypes = []string{spdxType}

				return p
			},
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(goodProvenance(t))
			},
			violations: []string{"no attestation of type " + spdxType},
		},
		{
			name: "required predicate type unsigned, foreign, refuted or about another digest",
			policy: func() *Policy {
				p := testPolicy(PolicyModeRequire)
				p.PredicateTypes = []string{spdxType}

				return p
			},
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(
					goodProvenance(t),
					newAttestation(t, sbomStatement(t, testDigest), nil),
					newAttestation(t, sbomStatement(t, testDigest), signed(t, foreignSigner)),
					newAttestation(t, sbomStatement(t, testDigest), refuted(t, testSigner)),
					newAttestation(t, sbomStatement(t, otherDigest), signed(t, testSigner)),
				)
			},
			violations: []string{"no attestation of type " + spdxType},
		},
		{
			name: "attestation without envelope",
			discovery: func(*testing.T) *Discovery {
				return newDiscovery(Attestation{Digest: testDigest, PredicateType: slsaV1Type, Location: "sha256-abc.att"})
			},
			violations: []string{"not a parseable in-toto statement"},
		},
	}
}

func TestPolicyEvaluatorEvaluate(t *testing.T) {
	t.Parallel()

	evaluator, err := NewPolicyEvaluator()
	require.NoError(t, err)

	for _, tc := range slices.Concat(
		provenanceTestCases(), builderSignerTestCases(), callerTestCases(), predicateTypeTestCases(),
	) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := testPolicy(PolicyModeRequire)
			if tc.policy != nil {
				policy = tc.policy()
			}

			require.NoError(t, policy.Validate())

			res, err := evaluator.Evaluate(context.Background(), tc.discovery(t), policy, "")
			require.NoError(t, err)

			require.Equal(t, tc.satisfied, res.Satisfied, "violations: %v", res.Violations)

			if tc.satisfied {
				require.Empty(t, res.Violations)
				require.NotEmpty(t, res.Provenance)
				require.Equal(t, tc.level, res.SLSALevel)

				if tc.provenance != "" {
					require.Equal(t, tc.provenance, res.Provenance,
						"the result names the provenance that sets the level")
				}

				return
			}

			joined := strings.Join(res.Violations, "\n")
			for _, want := range tc.violations {
				require.Contains(t, joined, want)
			}
		})
	}
}

func TestPolicyEvaluatorRealProvenance(t *testing.T) {
	t.Parallel()

	// The real, signed provenance of a Security Profiles Operator staging
	// image, verified against the sigstore trust root.
	data, err := os.ReadFile(filepath.Join("testdata", "spo-provenance.sigstore.json"))
	require.NoError(t, err)

	envs, err := (&bundle.Parser{}).Parse(data)
	require.NoError(t, err)
	require.Len(t, envs, 1)
	require.NoError(t, envs[0].Verify())

	const digest = "sha256:d2a142f2c558cf9c61acc09b68a39f105f5ec68f208d8b8894360098ae9b69a8"

	discovery := &Discovery{
		Reference: "us-central1-docker.pkg.dev/k8s-staging-images/sp-operator/security-profiles-operator-amd64@" + digest,
		Attestations: []Attestation{{
			Digest:         digest,
			Source:         SourceReferrer,
			PredicateType:  slsaV1Type,
			Status:         SignatureVerified,
			Signers:        []string{testSigner},
			SubjectMatches: true,
			Envelope:       envs[0],
		}},
	}

	evaluator, err := NewPolicyEvaluator()
	require.NoError(t, err)

	// The build signs its own provenance, which reaches every level 3
	// control but SLSA build level 1 only.
	policy := testPolicy(PolicyModeRequire)
	policy.Builders[0].Level = 1

	result, err := evaluator.Evaluate(context.Background(), discovery, policy, "")
	require.NoError(t, err)
	require.True(t, result.Satisfied, result.Violations)
	require.Equal(t, 1, result.SLSALevel)

	policy = testPolicy(PolicyModeRequire)
	policy.Builders = []Builder{{ID: "https://prow.k8s.io/post-security-profiles-operator-push-image", Level: 3}}

	result, err = evaluator.Evaluate(context.Background(), discovery, policy, "")
	require.NoError(t, err)
	require.False(t, result.Satisfied)
}

func TestPolicyEvaluatorAccepted(t *testing.T) {
	t.Parallel()

	provenance := newAttestation(t, provenanceStatement(t, provenanceOptions{}), signed(t, testSigner))
	sbom := newAttestation(t, sbomStatement(t, testDigest), signed(t, testSigner))

	discovery := newDiscovery(
		provenance,
		sbom,
		// Not accepted: another signer, unsigned, about another digest.
		newAttestation(t, sbomStatement(t, testDigest), signed(t, foreignSigner)),
		newAttestation(t, sbomStatement(t, testDigest), nil),
		newAttestation(t, sbomStatement(t, otherDigest), signed(t, testSigner)),
		// Not accepted: provenance by a policy signer that fails the
		// policy, from another source or an untrusted builder.
		newAttestation(t, provenanceStatement(t, provenanceOptions{source: "git+https://" + otherSourceRepo + "@refs/heads/main"}),
			signed(t, testSigner)),
		newAttestation(t, provenanceStatement(t, provenanceOptions{builder: untrustedBuilder}),
			signed(t, testSigner)),
	)

	evaluator, err := NewPolicyEvaluator()
	require.NoError(t, err)

	result, err := evaluator.Evaluate(context.Background(), discovery, testPolicy(PolicyModeRequire), "")
	require.NoError(t, err)
	require.True(t, result.Satisfied, result.Violations)
	require.Equal(t, []*Attestation{&discovery.Attestations[0], &discovery.Attestations[1]}, result.Accepted)
}

// platformAttestation returns an attestation attached to and about a
// platform manifest of the test index.
func platformAttestation(t *testing.T, data []byte, digest string) Attestation {
	t.Helper()

	att := newAttestation(t, data, signed(t, testSigner))
	att.Digest = digest

	return att
}

func TestPolicyEvaluatorPlatforms(t *testing.T) {
	t.Parallel()

	const thirdDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"

	evaluator, err := NewPolicyEvaluator()
	require.NoError(t, err)

	provenanceOf := func(t *testing.T, digest string, opts provenanceOptions) Attestation {
		t.Helper()

		opts.digest = digest

		return platformAttestation(t, provenanceStatement(t, opts), digest)
	}

	for _, tc := range []struct {
		name             string
		discovery        func(t *testing.T) *Discovery
		satisfied        bool
		throughPlatforms bool
		level            int
		violations       []string
	}{
		{
			name: "all platform manifests satisfy the policy",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(
					provenanceOf(t, otherDigest, provenanceOptions{}),
					provenanceOf(t, thirdDigest, provenanceOptions{noInvocation: true}),
				)
			},
			satisfied:        true,
			throughPlatforms: true,
			// The lowest level of the platform manifests.
			level: 2,
		},
		{
			name: "one platform manifest without provenance",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(provenanceOf(t, otherDigest, provenanceOptions{}))
			},
			violations: []string{
				"no SLSA build provenance found",
				"platform manifest " + thirdDigest + ": no SLSA build provenance found",
			},
		},
		{
			name: "one platform manifest from an untrusted builder",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(
					provenanceOf(t, otherDigest, provenanceOptions{}),
					provenanceOf(t, thirdDigest, provenanceOptions{builder: untrustedBuilder}),
				)
			},
			violations: []string{"platform manifest " + thirdDigest + ": "},
		},
		{
			name: "rejected provenance about the index is not outweighed by its platform manifests",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(
					newAttestation(t, provenanceStatement(t, provenanceOptions{builder: untrustedBuilder}),
						signed(t, testSigner)),
					provenanceOf(t, otherDigest, provenanceOptions{}),
					provenanceOf(t, thirdDigest, provenanceOptions{}),
				)
			},
			violations: []string{controlBuilderTrusted},
		},
		{
			name: "the index satisfies the policy on its own",
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				return newDiscovery(goodProvenance(t))
			},
			satisfied: true,
			level:     3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			discovery := tc.discovery(t)
			discovery.Children = []string{otherDigest, thirdDigest}

			// Level 2 lets provenance without an invocation pass.
			policy := testPolicy(PolicyModeRequire)
			policy.Level = 2

			res, err := evaluator.Evaluate(context.Background(), discovery, policy, "")
			require.NoError(t, err)
			require.Equal(t, tc.satisfied, res.Satisfied, "violations: %v", res.Violations)
			require.Equal(t, tc.throughPlatforms, res.ThroughPlatforms)
			require.Len(t, res.Platforms, 2)

			if tc.satisfied {
				require.Empty(t, res.Violations)
				require.Equal(t, tc.level, res.SLSALevel)

				return
			}

			joined := strings.Join(res.Violations, "\n")
			for _, want := range tc.violations {
				require.Contains(t, joined, want)
			}

			// Provenance of a platform manifest is not about the index.
			require.NotContains(t, joined, "not about")
		})
	}
}

func TestPolicyEvaluatorImageLevels(t *testing.T) {
	t.Parallel()

	const levelViolation = "its provenance verified at SLSA build level %d at most, the policy requires 3 for this image"

	generatorRelease := testGeneratorBuilder + "@refs/tags/v1.2.0"

	evaluator, err := NewPolicyEvaluator()
	require.NoError(t, err)

	for _, tc := range []struct {
		name       string
		image      string
		discovery  func(t *testing.T) *Discovery
		satisfied  bool
		level      int
		accepted   int
		violations []string
	}{
		{
			// The self-signed provenance passes as well and is carried.
			name:      "an image at its required level",
			image:     testLevelImage,
			discovery: claimedByBoth(testSelfSignedBuilder, generatorRelease),
			satisfied: true,
			level:     3,
			accepted:  2,
		},
		{
			name:       "an image below its required level",
			image:      testLevelImage,
			discovery:  claimedBy(testSelfSignedBuilder, testSigner),
			level:      1,
			accepted:   1,
			violations: []string{fmt.Sprintf(levelViolation, 1)},
		},
		{
			// The reason the level 3 provenance failed tells how to fix it.
			name:  "an image below its required level with rejected provenance",
			image: testLevelImage,
			discovery: func(t *testing.T) *Discovery {
				t.Helper()

				selfSigned := newAttestation(t,
					provenanceStatement(t, provenanceOptions{builder: testSelfSignedBuilder}), signed(t, testSigner))
				selfSigned.Location = bothLocations[0]

				// Only the generator signer may claim the generator.
				generator := newAttestation(t,
					provenanceStatement(t, provenanceOptions{builder: generatorRelease}), signed(t, testSigner))
				generator.Location = bothLocations[1]

				return newDiscovery(selfSigned, generator)
			},
			level:      1,
			accepted:   1,
			violations: []string{fmt.Sprintf(levelViolation, 1), bothLocations[1], controlBuilderTrusted},
		},
		{
			name:      "an image without a required level",
			image:     "security-profiles-operator-bundle",
			discovery: claimedBy(testSelfSignedBuilder, testSigner),
			satisfied: true,
			level:     1,
			accepted:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := boundBuilderPolicy(0)
			policy.Levels = []ImageLevel{{Images: []string{testLevelImage, testLevelPattern}, Level: 3}}
			require.NoError(t, policy.Validate())

			res, err := evaluator.Evaluate(context.Background(), tc.discovery(t), policy, tc.image)
			require.NoError(t, err)
			require.Equal(t, tc.satisfied, res.Satisfied, "violations: %v", res.Violations)
			require.Equal(t, tc.level, res.SLSALevel)
			require.Len(t, res.Accepted, tc.accepted)

			joined := strings.Join(res.Violations, "\n")
			for _, want := range tc.violations {
				require.Contains(t, joined, want)
			}
		})
	}

	// The platform manifests of an index are held to the level of the
	// index image.
	const thirdDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"

	discovery := newDiscovery(
		platformAttestation(t, provenanceStatement(t, provenanceOptions{digest: otherDigest}), otherDigest),
		platformAttestation(t, provenanceStatement(t, provenanceOptions{digest: thirdDigest, noInvocation: true}),
			thirdDigest),
	)
	discovery.Children = []string{otherDigest, thirdDigest}

	// Level 2 lets provenance without an invocation pass.
	policy := testPolicy(PolicyModeRequire)
	policy.Level = 2
	policy.Levels = []ImageLevel{{Images: []string{"img"}, Level: 3}}
	require.NoError(t, policy.Validate())

	res, err := evaluator.Evaluate(context.Background(), discovery, policy, "img")
	require.NoError(t, err)
	require.False(t, res.Satisfied)
	require.Contains(t, strings.Join(res.Violations, "\n"),
		"platform manifest "+thirdDigest+": "+fmt.Sprintf(levelViolation, 2))

	res, err = evaluator.Evaluate(context.Background(), discovery, policy, "other")
	require.NoError(t, err)
	require.True(t, res.Satisfied, res.Violations)
	require.True(t, res.ThroughPlatforms)
	require.Equal(t, 2, res.SLSALevel)

	// An index with provenance of its own passes on that alone, so its
	// platform manifests keep their own level.
	ownDiscovery := newDiscovery(
		newAttestation(t, provenanceStatement(t, provenanceOptions{builder: generatorRelease}),
			signed(t, testGeneratorSigner)),
		platformAttestation(t,
			provenanceStatement(t, provenanceOptions{digest: otherDigest, builder: testSelfSignedBuilder}), otherDigest),
	)
	ownDiscovery.Children = []string{otherDigest}

	ownPolicy := boundBuilderPolicy(0)
	ownPolicy.Levels = []ImageLevel{{Images: []string{testLevelImage}, Level: 3}}
	require.NoError(t, ownPolicy.Validate())

	res, err = evaluator.Evaluate(context.Background(), ownDiscovery, ownPolicy, testLevelImage)
	require.NoError(t, err)
	require.True(t, res.Satisfied, res.Violations)
	require.False(t, res.ThroughPlatforms)
	require.Equal(t, 3, res.SLSALevel)
	require.True(t, res.Platforms[otherDigest].Satisfied, res.Platforms[otherDigest].Violations)
	require.Equal(t, 1, res.Platforms[otherDigest].SLSALevel)
}

func TestPolicyEvaluatorPlatformAccepted(t *testing.T) {
	t.Parallel()

	provenance := platformAttestation(t, provenanceStatement(t, provenanceOptions{digest: otherDigest}), otherDigest)
	sbom := platformAttestation(t, sbomStatement(t, otherDigest), otherDigest)
	indexSBOM := newAttestation(t, sbomStatement(t, testDigest), signed(t, testSigner))

	discovery := newDiscovery(provenance, sbom, indexSBOM)
	discovery.Children = []string{otherDigest}

	evaluator, err := NewPolicyEvaluator()
	require.NoError(t, err)

	result, err := evaluator.Evaluate(context.Background(), discovery, testPolicy(PolicyModeRequire), "")
	require.NoError(t, err)
	require.True(t, result.ThroughPlatforms, result.Violations)

	// Each digest accepts the attestations about it.
	require.Equal(t, []*Attestation{&discovery.Attestations[2]}, result.Accepted)
	require.Equal(t, []*Attestation{&discovery.Attestations[0], &discovery.Attestations[1]},
		result.Platforms[otherDigest].Accepted)
}

func TestPolicyEvaluatorEvaluateErrors(t *testing.T) {
	t.Parallel()

	evaluator, err := NewPolicyEvaluator()
	require.NoError(t, err)

	ctx := context.Background()

	_, err = evaluator.Evaluate(ctx, nil, testPolicy(PolicyModeRequire), "")
	require.Error(t, err)

	_, err = evaluator.Evaluate(ctx, newDiscovery(), nil, "")
	require.Error(t, err)

	_, err = evaluator.Evaluate(ctx, &Discovery{Reference: "example.com/image:latest"}, testPolicy(PolicyModeRequire), "")
	require.ErrorContains(t, err, "parsing reference")

	_, err = evaluator.Evaluate(ctx, newDiscovery(), &Policy{Mode: PolicyModeRequire, Signers: []string{"bad"}}, "")
	require.ErrorContains(t, err, "invalid signer")

	_, err = evaluator.Evaluate(ctx, newDiscovery(), &Policy{Mode: PolicyModeRequire}, "")
	require.ErrorContains(t, err, "at least one signer")
}

func TestPolicyCheckerCheck(t *testing.T) {
	t.Parallel()

	good := func(t *testing.T) *Discovery {
		t.Helper()

		return newDiscovery(newAttestation(t, provenanceStatement(t, provenanceOptions{}), signed(t, testSigner)))
	}

	unsigned := func(t *testing.T) *Discovery {
		t.Helper()

		return newDiscovery(newAttestation(t, provenanceStatement(t, provenanceOptions{}), nil))
	}

	// A nil discovery means the discovery failed.
	failed := func(*testing.T) *Discovery { return nil }

	for _, tc := range []struct {
		name          string
		mode          PolicyMode
		discovery     func(t *testing.T) *Discovery
		wantErr       string
		wantSatisfied bool
		wantAccepted  int
		wantNoResult  bool
	}{
		{name: "off ignores the discovery", mode: PolicyModeOff, discovery: failed, wantNoResult: true},
		{
			name: "require satisfied", mode: PolicyModeRequire, discovery: good,
			wantSatisfied: true, wantAccepted: 1,
		},
		{name: "warn satisfied", mode: PolicyModeWarn, discovery: good, wantSatisfied: true, wantAccepted: 1},
		{
			name: "require violated", mode: PolicyModeRequire, discovery: unsigned,
			wantErr: "provenance policy not satisfied for " + testRef,
		},
		{name: "warn violated", mode: PolicyModeWarn, discovery: unsigned},
		{
			name: "require discovery failed", mode: PolicyModeRequire, discovery: failed,
			wantErr: "attestation discovery failed",
		},
		{name: "warn discovery failed", mode: PolicyModeWarn, discovery: failed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checker := &PolicyChecker{}

			result, err := checker.Check(context.Background(), testRef, testPolicy(tc.mode), "", tc.discovery(t))
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}

			if tc.wantNoResult {
				require.Nil(t, result)

				return
			}

			// The result is returned in require mode as well.
			require.NotNil(t, result)
			require.Equal(t, tc.wantSatisfied, result.Satisfied)
			require.Len(t, result.Accepted, tc.wantAccepted)
		})
	}
}

func TestPolicyCheckerImageLevels(t *testing.T) {
	t.Parallel()

	discovery := claimedBy(testSelfSignedBuilder, testSigner)(t)
	checker := &PolicyChecker{}

	for _, mode := range []PolicyMode{PolicyModeWarn, PolicyModeRequire} {
		policy := boundBuilderPolicy(0)
		policy.Mode = mode
		policy.Levels = []ImageLevel{{Images: []string{testLevelImage}, Level: 3}}

		result, err := checker.Check(context.Background(), testRef, policy, testLevelImage, discovery)
		require.NotNil(t, result)
		require.False(t, result.Satisfied)

		if mode == PolicyModeRequire {
			require.ErrorContains(t, err, "the policy requires 3 for this image")
		} else {
			require.NoError(t, err, "warn mode only logs the violation")
		}

		result, err = checker.Check(context.Background(), testRef, policy, "bundle", discovery)
		require.NoError(t, err)
		require.True(t, result.Satisfied)
	}
}

func TestPolicyCheckerOtherImage(t *testing.T) {
	t.Parallel()

	for _, discovery := range []*Discovery{
		{Reference: strings.Replace(testRef, testDigest, otherDigest, 1)},
		{Reference: "example.com/other@" + testDigest},
	} {
		checker := &PolicyChecker{}

		for _, mode := range []PolicyMode{PolicyModeWarn, PolicyModeRequire} {
			_, err := checker.Check(context.Background(), testRef, testPolicy(mode), "", discovery)
			require.ErrorContains(t, err, "returned another image")
		}
	}
}

func TestPolicyCheckerWithoutPolicy(t *testing.T) {
	t.Parallel()

	checker := &PolicyChecker{}

	result, err := checker.Check(context.Background(), testRef, nil, "", nil)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestImageProvenanceAcceptedByAll(t *testing.T) {
	t.Parallel()

	att := &Attestation{Location: "staging/image@sha256:aaa", Layer: "sha256:bbb"}
	same := &Attestation{Location: att.Location, Layer: att.Layer}
	other := &Attestation{Location: att.Location, Layer: "sha256:ccc"}

	accepting := &PolicyResult{Satisfied: true, Accepted: []*Attestation{same}}

	var none *ImageProvenance

	require.False(t, none.Satisfied())
	require.False(t, (&ImageProvenance{}).Satisfied(), "an image without a policy satisfies none")
	require.False(t, none.AcceptedByAll(att))

	all := &ImageProvenance{Results: []*PolicyResult{accepting, accepting}}
	require.True(t, all.Satisfied())
	require.True(t, all.AcceptedByAll(att), "the same referrer layer counts")
	require.False(t, all.AcceptedByAll(other))

	partial := &ImageProvenance{Results: []*PolicyResult{accepting, {Satisfied: true}}}
	require.True(t, partial.Satisfied())
	require.False(t, partial.AcceptedByAll(att), "every policy must accept it")

	unsatisfied := &ImageProvenance{Results: []*PolicyResult{accepting, {Satisfied: false, Accepted: []*Attestation{same}}}}
	require.False(t, unsatisfied.Satisfied())
	require.False(t, unsatisfied.AcceptedByAll(att))
	require.False(t, (&ImageProvenance{Results: []*PolicyResult{nil}}).Satisfied())
}
