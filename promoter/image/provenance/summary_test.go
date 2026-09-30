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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/carabiner-dev/collector/envelope/bare"
	intoto "github.com/in-toto/attestation/go/v1"
	"github.com/stretchr/testify/require"
)

const (
	// testViolation is a policy violation.
	testViolation = "the build provenance is missing"

	summaryName   = "registry.k8s.io/security-profiles-operator/security-profiles-operator"
	summarySource = "us-central1-docker.pkg.dev/k8s-staging-images/sp-operator/security-profiles-operator"

	// Verified build levels.
	levelBuild2 = "SLSA_BUILD_LEVEL_2"
	levelBuild3 = "SLSA_BUILD_LEVEL_3"

	// Digest algorithms of in-toto digest sets.
	algorithmSHA256    = "sha256"
	algorithmGitCommit = "gitCommit"
)

// summaryStatement is the part of a verification summary statement the
// tests check.
type summaryStatement struct {
	Subject []struct {
		Name   string            `json:"name"`
		Digest map[string]string `json:"digest"`
	} `json:"subject"`
	PredicateType string `json:"predicateType"`
	Predicate     struct {
		Verifier struct {
			ID      string            `json:"id"`
			Version map[string]string `json:"version"`
		} `json:"verifier"`
		TimeVerified string `json:"timeVerified"`
		ResourceURI  string `json:"resourceUri"`
		Policy       *struct {
			URI    string            `json:"uri"`
			Digest map[string]string `json:"digest"`
		} `json:"policy"`
		InputAttestations []struct {
			URI    string            `json:"uri"`
			Digest map[string]string `json:"digest"`
		} `json:"inputAttestations"`
		VerificationResult string   `json:"verificationResult"`
		VerifiedLevels     []string `json:"verifiedLevels"`
		SLSAVersion        string   `json:"slsaVersion"`
	} `json:"predicate"`
}

// summaryOf builds and parses the verification summary of the input.
func summaryOf(t *testing.T, in *SummaryInput) *summaryStatement {
	t.Helper()

	data, err := VerificationSummary(in)
	require.NoError(t, err)

	stmt := &summaryStatement{}
	require.NoError(t, json.Unmarshal(data, stmt))

	return stmt
}

// summaryManifest is the promoter manifest of the test summaries.
func summaryManifest() *ResourceDescriptor {
	return &ResourceDescriptor{
		Name:   "registry.k8s.io/manifests/k8s-staging-sp-operator/promoter-manifest.yaml",
		Uri:    "git+https://github.com/kubernetes/k8s.io",
		Digest: map[string]string{algorithmGitCommit: strings.Repeat("c", 40)},
	}
}

// summaryInput returns an input for the test digest with one source image.
func summaryInput(outcome *ImageProvenance, discovery *Discovery) *SummaryInput {
	return &SummaryInput{
		Name:    summaryName,
		Digest:  testDigest,
		Version: "v4.7.0",
		Time:    time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
		Policy:  summaryManifest(),
		Sources: []SummarySource{{Discovery: discovery, Provenance: outcome}},
	}
}

func TestVerificationSummaryWithoutPolicy(t *testing.T) {
	t.Parallel()

	stmt := summaryOf(t, summaryInput(&ImageProvenance{}, &Discovery{Reference: summarySource + "@" + testDigest}))

	require.Equal(t, SummaryPredicateType, stmt.PredicateType)
	require.Len(t, stmt.Subject, 1)
	require.Equal(t, summaryName, stmt.Subject[0].Name)
	require.Equal(t, map[string]string{algorithmSHA256: strings.TrimPrefix(testDigest, "sha256:")}, stmt.Subject[0].Digest)

	pred := stmt.Predicate
	require.Equal(t, SummaryVerifierID, pred.Verifier.ID)
	require.Equal(t, map[string]string{"kpromo": "v4.7.0"}, pred.Verifier.Version)
	require.Equal(t, "2026-09-29T12:00:00Z", pred.TimeVerified)
	require.Equal(t, summaryName+"@"+testDigest, pred.ResourceURI)
	require.NotNil(t, pred.Policy)
	require.Equal(t,
		"git+https://github.com/kubernetes/k8s.io#registry.k8s.io/manifests/k8s-staging-sp-operator/promoter-manifest.yaml",
		pred.Policy.URI)
	require.Equal(t, map[string]string{algorithmGitCommit: strings.Repeat("c", 40)}, pred.Policy.Digest)
	require.Empty(t, pred.InputAttestations)
	require.Equal(t, resultPassed, pred.VerificationResult)
	require.Equal(t, []string{LevelBuildUnevaluated, LevelManifestReviewed}, pred.VerifiedLevels)
	require.Equal(t, "1.0", pred.SLSAVersion)
}

func TestVerificationSummaryLevels(t *testing.T) {
	t.Parallel()

	satisfied := func(level int) *PolicyResult { return &PolicyResult{Satisfied: true, SLSALevel: level} }

	for _, tc := range []struct {
		name    string
		results []*PolicyResult
		result  string
		levels  []string
	}{
		{
			name:    "one policy",
			results: []*PolicyResult{satisfied(3)},
			result:  resultPassed,
			levels:  []string{levelBuild3, LevelManifestReviewed},
		},
		{
			name:    "the lowest level of all policies",
			results: []*PolicyResult{satisfied(3), satisfied(2)},
			result:  resultPassed,
			levels:  []string{levelBuild2, LevelManifestReviewed},
		},
		{
			name:    "a level that was not verified is never claimed",
			results: []*PolicyResult{satisfied(0)},
			result:  resultPassed,
			levels:  []string{LevelBuildUnevaluated, LevelManifestReviewed},
		},
		{
			name:    "not even next to a verified level",
			results: []*PolicyResult{satisfied(0), satisfied(3)},
			result:  resultPassed,
			levels:  []string{LevelBuildUnevaluated, LevelManifestReviewed},
		},
		{
			name:    "in any order",
			results: []*PolicyResult{satisfied(3), satisfied(0)},
			result:  resultPassed,
			levels:  []string{LevelBuildUnevaluated, LevelManifestReviewed},
		},
		{
			name:    "a policy in warn mode that is not satisfied",
			results: []*PolicyResult{satisfied(3), {Violations: []string{testViolation}}},
			result:  resultFailed,
			levels:  []string{levelFailed},
		},
		{
			name:    "a missing result is not satisfied",
			results: []*PolicyResult{nil},
			result:  resultFailed,
			levels:  []string{levelFailed},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policies := make([]*Policy, len(tc.results))
			stmt := summaryOf(t, summaryInput(&ImageProvenance{Policies: policies, Results: tc.results}, nil))

			require.Equal(t, tc.result, stmt.Predicate.VerificationResult)
			require.Equal(t, tc.levels, stmt.Predicate.VerifiedLevels)
		})
	}
}

func TestVerificationSummarySources(t *testing.T) {
	t.Parallel()

	satisfied := &ImageProvenance{Results: []*PolicyResult{{Satisfied: true, SLSALevel: 3}}}
	failed := &ImageProvenance{Results: []*PolicyResult{{Violations: []string{testViolation}}}}

	for _, tc := range []struct {
		name     string
		outcomes []*ImageProvenance
		result   string
		levels   []string
	}{
		{
			name:     "all sources satisfied",
			outcomes: []*ImageProvenance{satisfied, satisfied},
			result:   resultPassed,
			levels:   []string{levelBuild3, LevelManifestReviewed},
		},
		{
			name:     "a source without a policy",
			outcomes: []*ImageProvenance{satisfied, {}},
			result:   resultPassed,
			levels:   []string{LevelBuildUnevaluated, LevelManifestReviewed},
		},
		{
			name:     "a source not satisfied",
			outcomes: []*ImageProvenance{{}, failed},
			result:   resultFailed,
			levels:   []string{levelFailed},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := summaryInput(nil, nil)
			in.Sources = nil

			for _, outcome := range tc.outcomes {
				in.Sources = append(in.Sources, SummarySource{Provenance: outcome})
			}

			stmt := summaryOf(t, in)
			require.Equal(t, tc.result, stmt.Predicate.VerificationResult)
			require.Equal(t, tc.levels, stmt.Predicate.VerifiedLevels)
		})
	}
}

func TestChildSummaryInput(t *testing.T) {
	t.Parallel()

	child := "sha256:" + strings.Repeat("9", 64)
	accepted := &Attestation{
		Source: SourceReferrer, Location: "sha256:" + strings.Repeat("a", 64), Layer: "sha256:" + strings.Repeat("e", 64),
	}

	// The provenance of the index is not about the child: no level, no
	// input attestations.
	index := summaryInput(&ImageProvenance{
		Policies: []*Policy{{}},
		Results:  []*PolicyResult{{Satisfied: true, SLSALevel: 3, Accepted: []*Attestation{accepted}}},
	}, &Discovery{Reference: summarySource + "@" + testDigest})

	stmt := summaryOf(t, ChildSummaryInput(child, []*SummaryInput{index}))
	require.Equal(t, summaryName+"@"+child, stmt.Predicate.ResourceURI)
	require.Equal(t, strings.Repeat("9", 64), stmt.Subject[0].Digest[algorithmSHA256])
	require.Equal(t, resultPassed, stmt.Predicate.VerificationResult)
	require.Equal(t, []string{LevelBuildUnevaluated, LevelManifestReviewed}, stmt.Predicate.VerifiedLevels)
	require.Empty(t, stmt.Predicate.InputAttestations)
	require.NotNil(t, stmt.Predicate.Policy)

	require.Equal(t, policyURI(summaryManifest()), stmt.Predicate.Policy.URI)

	// It fails when one of its indexes fails, and names the repository
	// when they come from different manifests.
	failed := summaryInput(&ImageProvenance{Results: []*PolicyResult{{Violations: []string{testViolation}}}}, nil)
	failed.Policy.Name = "other/promoter-manifest.yaml"
	stmt = summaryOf(t, ChildSummaryInput(child, []*SummaryInput{index, failed}))
	require.Equal(t, resultFailed, stmt.Predicate.VerificationResult)
	require.Equal(t, []string{levelFailed}, stmt.Predicate.VerifiedLevels)
	require.Equal(t, summaryManifest().GetUri(), stmt.Predicate.Policy.URI)

	require.Nil(t, ChildSummaryInput(child, nil))
}

func TestChildSummaryInputPlatformResults(t *testing.T) {
	t.Parallel()

	child := "sha256:" + strings.Repeat("9", 64)
	discovery := &Discovery{Reference: summarySource + "@" + testDigest}
	platformProvenance := &Attestation{
		Source: SourceReferrer, Location: "sha256:" + strings.Repeat("a", 64), Layer: "sha256:" + strings.Repeat("e", 64),
	}

	index := func(indexSatisfied bool, platform *PolicyResult) *SummaryInput {
		return summaryInput(&ImageProvenance{
			Policies: []*Policy{{}},
			Results: []*PolicyResult{{
				Satisfied:        indexSatisfied || platform.Satisfied,
				SLSALevel:        2,
				ThroughPlatforms: !indexSatisfied && platform.Satisfied,
				Platforms:        map[string]*PolicyResult{child: platform},
			}},
		}, discovery)
	}

	satisfied := &PolicyResult{Satisfied: true, SLSALevel: 2, Accepted: []*Attestation{platformProvenance}}
	violated := &PolicyResult{Violations: []string{testViolation}}

	// The platform manifest claims the level of its own provenance.
	stmt := summaryOf(t, ChildSummaryInput(child, []*SummaryInput{index(false, satisfied)}))
	require.Equal(t, resultPassed, stmt.Predicate.VerificationResult)
	require.Equal(t, []string{levelBuild2, LevelManifestReviewed}, stmt.Predicate.VerifiedLevels)
	require.Len(t, stmt.Predicate.InputAttestations, 1)
	require.Equal(t, summarySource+"@"+platformProvenance.Location, stmt.Predicate.InputAttestations[0].URI)

	// An index with provenance of its own passes its platform manifests
	// without claiming a level for them.
	stmt = summaryOf(t, ChildSummaryInput(child, []*SummaryInput{index(true, violated)}))
	require.Equal(t, resultPassed, stmt.Predicate.VerificationResult)
	require.Equal(t, []string{LevelBuildUnevaluated, LevelManifestReviewed}, stmt.Predicate.VerifiedLevels)
	require.Empty(t, stmt.Predicate.InputAttestations)

	// A platform manifest with passing attestations of its own claims its
	// level, whether or not the index passed on its own.
	stmt = summaryOf(t, ChildSummaryInput(child, []*SummaryInput{index(true, satisfied)}))
	require.Equal(t, []string{levelBuild2, LevelManifestReviewed}, stmt.Predicate.VerifiedLevels)

	// Neither the index nor the platform manifest satisfy the policy.
	stmt = summaryOf(t, ChildSummaryInput(child, []*SummaryInput{index(false, violated)}))
	require.Equal(t, resultFailed, stmt.Predicate.VerificationResult)
}

func TestVerificationSummaryThroughPlatforms(t *testing.T) {
	t.Parallel()

	child := "sha256:" + strings.Repeat("9", 64)
	indexSBOM := &Attestation{
		Source: SourceReferrer, Location: "sha256:" + strings.Repeat("a", 64), Layer: "sha256:" + strings.Repeat("e", 64),
	}
	platformProvenance := &Attestation{
		Source: SourceReferrer, Location: "sha256:" + strings.Repeat("b", 64), Layer: "sha256:" + strings.Repeat("f", 64),
	}

	// The index was verified with the provenance of its platform
	// manifests, so the summary lists it.
	stmt := summaryOf(t, summaryInput(&ImageProvenance{
		Policies: []*Policy{{}},
		Results: []*PolicyResult{{
			Satisfied:        true,
			SLSALevel:        3,
			ThroughPlatforms: true,
			Accepted:         []*Attestation{indexSBOM},
			Platforms: map[string]*PolicyResult{
				child: {Satisfied: true, SLSALevel: 3, Accepted: []*Attestation{platformProvenance}},
			},
		}},
	}, &Discovery{Reference: summarySource + "@" + testDigest}))

	require.Equal(t, []string{levelBuild3, LevelManifestReviewed}, stmt.Predicate.VerifiedLevels)
	require.Len(t, stmt.Predicate.InputAttestations, 2)
	require.Equal(t, summarySource+"@"+indexSBOM.Location, stmt.Predicate.InputAttestations[0].URI)
	require.Equal(t, summarySource+"@"+platformProvenance.Location, stmt.Predicate.InputAttestations[1].URI)
}

func TestImageProvenancePlatform(t *testing.T) {
	t.Parallel()

	child := "sha256:" + strings.Repeat("9", 64)
	platform := &PolicyResult{Satisfied: true}
	policies := []*Policy{{Mode: PolicyModeWarn}, {Mode: PolicyModeRequire}}

	outcome := &ImageProvenance{
		Policies: policies,
		Results: []*PolicyResult{
			{Platforms: map[string]*PolicyResult{child: platform}},
			{Platforms: map[string]*PolicyResult{child: platform}},
		},
	}
	require.Equal(t, &ImageProvenance{Policies: policies, Results: []*PolicyResult{platform, platform}}, outcome.Platform(child))

	// Every policy needs a result for the platform manifest.
	outcome.Results[1].Platforms = nil
	require.Nil(t, outcome.Platform(child))
	require.Nil(t, (&ImageProvenance{}).Platform(child))

	var none *ImageProvenance
	require.Nil(t, none.Platform(child))
}

func TestVerificationSummaryRepositoryPolicy(t *testing.T) {
	t.Parallel()

	// Policies from several manifests are described by their repository.
	in := summaryInput(&ImageProvenance{}, nil)
	in.Policy.Name = ""

	stmt := summaryOf(t, in)
	require.Equal(t, "git+https://github.com/kubernetes/k8s.io", stmt.Predicate.Policy.URI)
	require.Equal(t, map[string]string{algorithmGitCommit: strings.Repeat("c", 40)}, stmt.Predicate.Policy.Digest)
}

func TestVerificationSummaryInputAttestations(t *testing.T) {
	t.Parallel()

	referrer := &Attestation{
		Source:   SourceReferrer,
		Location: "sha256:" + strings.Repeat("a", 64),
		Layer:    "sha256:" + strings.Repeat("e", 64),
	}
	other := &Attestation{
		Source:   SourceReferrer,
		Location: "sha256:" + strings.Repeat("b", 64),
		Layer:    "sha256:" + strings.Repeat("f", 64),
	}

	// An attestation tag without a known payload digest can't be
	// identified.
	tag := &Attestation{Source: SourceAttestationTag, Location: "sha256-" + strings.Repeat("1", 64) + ".att"}

	outcome := &ImageProvenance{
		Policies: []*Policy{{}, {}},
		Results: []*PolicyResult{
			{Satisfied: true, SLSALevel: 3, Accepted: []*Attestation{referrer, tag}},
			{Satisfied: true, SLSALevel: 3, Accepted: []*Attestation{referrer, other}},
		},
	}

	stmt := summaryOf(t, summaryInput(outcome, &Discovery{Reference: summarySource + "@" + testDigest}))

	inputs := stmt.Predicate.InputAttestations
	require.Len(t, inputs, 2, "each accepted attestation once")
	require.Equal(t, summarySource+"@"+referrer.Location, inputs[0].URI)
	require.Equal(t, map[string]string{algorithmSHA256: strings.Repeat("e", 64)}, inputs[0].Digest,
		"the digest of the bundle")
	require.Equal(t, summarySource+"@"+other.Location, inputs[1].URI)

	// An attestation tag is identified by the digest of its payload.
	envs, err := bare.New().ParseStream(strings.NewReader(string(sbomStatement(t, testDigest))))
	require.NoError(t, err)
	require.Len(t, envs, 1)

	payload := map[string]string{algorithmSHA256: strings.Repeat("d", 64)}
	envs[0].GetPredicate().SetOrigin(&intoto.ResourceDescriptor{Digest: payload})
	tag.Envelope = envs[0]

	stmt = summaryOf(t, summaryInput(outcome, &Discovery{Reference: summarySource + "@" + testDigest}))
	inputs = stmt.Predicate.InputAttestations
	require.Len(t, inputs, 3)
	require.Equal(t, summarySource+":"+tag.Location, inputs[1].URI)
	require.Equal(t, payload, inputs[1].Digest)

	// Without a discovery nothing was accepted.
	stmt = summaryOf(t, summaryInput(outcome, nil))
	require.Empty(t, stmt.Predicate.InputAttestations)
}

func TestVerificationSummaryErrors(t *testing.T) {
	t.Parallel()

	_, err := VerificationSummary(summaryInput(nil, nil))
	require.ErrorContains(t, err, "no provenance result")

	in := summaryInput(&ImageProvenance{}, nil)
	in.Sources = nil
	_, err = VerificationSummary(in)
	require.ErrorContains(t, err, "no source image")

	in = summaryInput(&ImageProvenance{}, nil)
	in.Digest = "latest"
	_, err = VerificationSummary(in)
	require.ErrorContains(t, err, "invalid digest")

	// A summary claims a reviewed promoter manifest.
	for _, policy := range []*ResourceDescriptor{
		nil,
		{Name: "promoter-manifest.yaml"},
		{Name: "promoter-manifest.yaml", Uri: "git+https://github.com/kubernetes/k8s.io"},
	} {
		in = summaryInput(&ImageProvenance{}, nil)
		in.Policy = policy
		_, err = VerificationSummary(in)
		require.ErrorContains(t, err, "no promoter manifest at a commit")
	}
}
