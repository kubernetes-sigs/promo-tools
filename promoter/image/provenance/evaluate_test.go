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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carabiner-dev/collector/envelope/bare"
	"github.com/carabiner-dev/collector/envelope/bundle"
	sapi "github.com/carabiner-dev/signer/api/v1"
	"github.com/stretchr/testify/require"
)

const (
	testDigest      = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	otherDigest     = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	testRef         = "us-central1-docker.pkg.dev/k8s-staging-images/sp-operator/security-profiles-operator@" + testDigest
	foreignSigner   = "sigstore::https://accounts.google.com::someone-else@example.iam.gserviceaccount.com"
	spdxType        = "https://spdx.dev/Document"
	slsaV1Type      = "https://slsa.dev/provenance/v1"
	testBuildType   = "https://cloudbuild.googleapis.com/CloudBuildYaml@v1"
	testInvocation  = "https://prow.k8s.io/view/gs/kubernetes-ci-logs/logs/post-security-profiles-operator-push-image/1"
	testSourceURI   = "git+https://" + testSource + "@refs/heads/main"
	otherSourceRepo = "github.com/kubernetes-sigs/other"
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

	data, err := json.Marshal(inTotoStatement{
		Type:          inTotoStatementType,
		PredicateType: slsaV1Type,
		Subject:       subjectsFor(opts.digest),
		Predicate: map[string]any{
			"buildDefinition": map[string]any{
				"buildType":          testBuildType,
				"externalParameters": map[string]any{},
				"resolvedDependencies": []map[string]any{
					{"uri": opts.source, "digest": map[string]string{"gitCommit": strings.Repeat("a", 40)}},
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

// evaluateTestCase is a policy evaluation test case.
type evaluateTestCase struct {
	name       string
	policy     func() *Policy
	discovery  func(t *testing.T) *Discovery
	satisfied  bool
	level      int
	violations []string
}

// goodProvenance returns build provenance that satisfies testPolicy.
func goodProvenance(t *testing.T) Attestation {
	t.Helper()

	return newAttestation(t, provenanceStatement(t, provenanceOptions{}), signed(t, testSigner))
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
			violations: []string{"builder-id-trusted"},
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
				return newDiscovery(Attestation{PredicateType: slsaV1Type, Location: "sha256-abc.att"})
			},
			violations: []string{"not a parseable in-toto statement"},
		},
	}
}

func TestPolicyEvaluatorEvaluate(t *testing.T) {
	t.Parallel()

	evaluator, err := NewPolicyEvaluator()
	require.NoError(t, err)

	for _, tc := range append(provenanceTestCases(), predicateTypeTestCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := testPolicy(PolicyModeRequire)
			if tc.policy != nil {
				policy = tc.policy()
			}

			require.NoError(t, policy.Validate())

			res, err := evaluator.Evaluate(context.Background(), tc.discovery(t), policy)
			require.NoError(t, err)

			require.Equal(t, tc.satisfied, res.Satisfied, "violations: %v", res.Violations)

			if tc.satisfied {
				require.Empty(t, res.Violations)
				require.NotEmpty(t, res.Provenance)
				require.Equal(t, tc.level, res.SLSALevel)

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

	result, err := evaluator.Evaluate(context.Background(), discovery, testPolicy(PolicyModeRequire))
	require.NoError(t, err)
	require.True(t, result.Satisfied, result.Violations)
	require.Equal(t, 3, result.SLSALevel)

	policy := testPolicy(PolicyModeRequire)
	policy.Builders = []string{"https://prow.k8s.io/post-security-profiles-operator-push-image"}

	result, err = evaluator.Evaluate(context.Background(), discovery, policy)
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
		newAttestation(t, provenanceStatement(t, provenanceOptions{builder: "https://prow.k8s.io/untrusted"}),
			signed(t, testSigner)),
	)

	evaluator, err := NewPolicyEvaluator()
	require.NoError(t, err)

	result, err := evaluator.Evaluate(context.Background(), discovery, testPolicy(PolicyModeRequire))
	require.NoError(t, err)
	require.True(t, result.Satisfied, result.Violations)
	require.Equal(t, []*Attestation{&discovery.Attestations[0], &discovery.Attestations[1]}, result.Accepted)
}

func TestPolicyEvaluatorEvaluateErrors(t *testing.T) {
	t.Parallel()

	evaluator, err := NewPolicyEvaluator()
	require.NoError(t, err)

	ctx := context.Background()

	_, err = evaluator.Evaluate(ctx, nil, testPolicy(PolicyModeRequire))
	require.Error(t, err)

	_, err = evaluator.Evaluate(ctx, newDiscovery(), nil)
	require.Error(t, err)

	_, err = evaluator.Evaluate(ctx, &Discovery{Reference: "example.com/image:latest"}, testPolicy(PolicyModeRequire))
	require.ErrorContains(t, err, "parsing reference")

	_, err = evaluator.Evaluate(ctx, newDiscovery(), &Policy{Mode: PolicyModeRequire, Signers: []string{"bad"}})
	require.ErrorContains(t, err, "invalid signer")

	_, err = evaluator.Evaluate(ctx, newDiscovery(), &Policy{Mode: PolicyModeRequire})
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

			result, err := checker.Check(context.Background(), testRef, testPolicy(tc.mode), tc.discovery(t))
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

func TestPolicyCheckerOtherImage(t *testing.T) {
	t.Parallel()

	for _, discovery := range []*Discovery{
		{Reference: strings.Replace(testRef, testDigest, otherDigest, 1)},
		{Reference: "example.com/other@" + testDigest},
	} {
		checker := &PolicyChecker{}

		for _, mode := range []PolicyMode{PolicyModeWarn, PolicyModeRequire} {
			_, err := checker.Check(context.Background(), testRef, testPolicy(mode), discovery)
			require.ErrorContains(t, err, "returned another image")
		}
	}
}

func TestPolicyCheckerWithoutPolicy(t *testing.T) {
	t.Parallel()

	checker := &PolicyChecker{}

	result, err := checker.Check(context.Background(), testRef, nil, nil)
	require.NoError(t, err)
	require.Nil(t, result)
}
