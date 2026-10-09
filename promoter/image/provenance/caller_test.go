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

	"github.com/stretchr/testify/require"
)

const (
	// testCallerWorkflow is the release workflow that calls the provenance
	// generator, and testRepository the repository of both.
	testCallerWorkflow = ".github/workflows/build.yml"
	testRepository     = "https://" + testSource
	testReleaseRef     = "refs/tags/v1.2.0"

	// testReleaseRefs is the ref pattern of release tags, and
	// refsViolation the violation of a caller at another ref.
	testReleaseRefs = "refs/tags/v*"
	refsViolation   = "the builder allows the refs " + testReleaseRefs

	// uriField and digestField name the URI and the digest of a resource
	// descriptor.
	uriField    = "uri"
	digestField = "digest"
)

// testCaller allows the release workflow to call the generator for
// release tags.
func testCaller() *Caller {
	return &Caller{Workflows: []string{testCallerWorkflow}, Refs: []string{testReleaseRefs}}
}

// refsOnlyCallerPolicy returns callerPolicy without the workflows.
func refsOnlyCallerPolicy() *Policy {
	p := callerPolicy()
	p.Builders[1].Caller.Workflows = nil

	return p
}

// callerPolicy returns boundBuilderPolicy with the caller constraints of
// testCaller on the provenance generator.
func callerPolicy() *Policy {
	p := boundBuilderPolicy(0)
	p.Builders[1].Caller = testCaller()

	return p
}

// release is the run of the release workflow for the release tag.
func release() *workflowRun {
	return &workflowRun{Repository: testRepository, Path: testCallerWorkflow, Ref: testReleaseRef}
}

// calledBy returns a discovery with provenance of the generator that names
// the workflow as its caller, signed by the generator in a run whose
// certificate names the run as its Build Config URI. A nil workflow or run
// leaves it out of the provenance or the certificate.
func calledBy(workflow, run *workflowRun) func(t *testing.T) *Discovery {
	repository := ""
	if run != nil {
		repository = run.Repository
	}

	return calledFrom(workflow, run, repository)
}

// calledFrom is calledBy with the source repository of the certificate,
// which is left out when empty.
func calledFrom(workflow, run *workflowRun, repository string) func(t *testing.T) *Discovery {
	return func(t *testing.T) *Discovery {
		t.Helper()

		verification := signed(t, testGeneratorSigner)

		if run != nil {
			identity := verification.GetSignature().GetIdentities()[0].GetSigstore()
			identity.BuildConfigUri = run.String()
			identity.SourceRepositoryUri = repository
		}

		return newDiscovery(newAttestation(t, provenanceStatement(t, provenanceOptions{
			builder:  testGeneratorBuilder + "@" + testReleaseRef,
			workflow: workflow,
		}), verification))
	}
}

// v02CalledBy returns a discovery with SLSA v0.2 provenance of the
// generator, written like the SLSA GitHub generator writes it, whose config
// source names the workflow as its caller, signed in a run whose
// certificate names the run as its Build Config URI.
func v02CalledBy(workflow, run *workflowRun) func(t *testing.T) *Discovery {
	return func(t *testing.T) *Discovery {
		t.Helper()

		verification := signed(t, testGeneratorSigner)
		identity := verification.GetSignature().GetIdentities()[0].GetSigstore()
		identity.BuildConfigUri = run.String()
		identity.SourceRepositoryUri = run.Repository

		source := "git+" + workflow.Repository + "@" + workflow.Ref
		commit := map[string]string{"sha1": strings.Repeat("a", 40)}

		data, err := json.Marshal(inTotoStatement{
			Type:          inTotoStatementType,
			PredicateType: "https://slsa.dev/provenance/v0.2",
			Subject:       subjectsFor(testDigest),
			Predicate: map[string]any{
				"builder":   map[string]any{"id": testGeneratorBuilder + "@" + testReleaseRef},
				"buildType": "https://github.com/slsa-framework/slsa-github-generator/generic@v1",
				"invocation": map[string]any{
					"configSource": map[string]any{uriField: source, digestField: commit, "entryPoint": workflow.Path},
				},
				"metadata":  map[string]any{"buildInvocationID": testInvocation},
				"materials": []map[string]any{{uriField: source, digestField: commit}},
			},
		})
		require.NoError(t, err)

		return newDiscovery(newAttestation(t, data, verification))
	}
}

// v02CalledAs returns a discovery with SLSA v0.2 provenance and a
// certificate that both name the run as the caller.
func v02CalledAs(run *workflowRun) func(t *testing.T) *Discovery {
	return v02CalledBy(run, run)
}

// withMalformedCaller returns a discovery with provenance of the
// generator at the release ref, whose calling workflow is not an object.
func withMalformedCaller(t *testing.T) *Discovery {
	t.Helper()

	var statement map[string]any
	require.NoError(t, json.Unmarshal(provenanceStatement(t, provenanceOptions{
		builder: testGeneratorBuilder + "@" + testReleaseRef,
	}), &statement))

	predicate, ok := statement["predicate"].(map[string]any)
	require.True(t, ok)
	definition, ok := predicate["buildDefinition"].(map[string]any)
	require.True(t, ok)

	definition["externalParameters"] = map[string]any{"workflow": "not a workflow"}

	data, err := json.Marshal(statement)
	require.NoError(t, err)

	return newDiscovery(newAttestation(t, data, signed(t, testGeneratorSigner)))
}

// withSources returns callerPolicy with other spellings of its source.
func withSources(sources ...string) func() *Policy {
	return func() *Policy {
		p := callerPolicy()
		p.Sources = sources

		return p
	}
}

// pinnedCallerPolicy returns boundBuilderPolicy with the caller
// constraints on the generator at another release, next to the generator
// at any ref without them.
func pinnedCallerPolicy() *Policy {
	p := boundBuilderPolicy(0)
	p.Builders = append(p.Builders, Builder{
		ID: testGeneratorBuilder + "@refs/tags/v0.9.0", Level: 3,
		Signers: []string{testGeneratorSigner}, Caller: testCaller(),
	})

	return p
}

// withRun returns the release run, changed by the function.
func withRun(change func(*workflowRun)) *workflowRun {
	run := release()
	change(run)

	return run
}

// releaseAt and releaseOf return the release run at another ref and of
// another workflow, and foreignRelease the release run in another
// repository.
func releaseAt(ref string) *workflowRun {
	return withRun(func(r *workflowRun) { r.Ref = ref })
}

func releaseOf(workflow string) *workflowRun {
	return withRun(func(r *workflowRun) { r.Path = workflow })
}

func foreignRelease() *workflowRun {
	return withRun(func(r *workflowRun) { r.Repository = otherRepositoryURL })
}

// calledAs returns a discovery with provenance and certificate that both
// name the run as the caller.
func calledAs(run *workflowRun) func(t *testing.T) *Discovery {
	return calledBy(run, run)
}

const (
	otherWorkflow      = ".github/workflows/other.yml"
	notWorkflowAtRef   = "is not a GitHub workflow at a ref"
	otherRepositoryURL = "https://" + otherSourceRepo
)

// callerTestCases cover the caller constraints of builders, with
// callerPolicy unless they set another.
func callerTestCases() []evaluateTestCase {
	cases := []evaluateTestCase{
		{
			name:      "provenance of an allowed caller verifies at the builder level",
			discovery: calledAs(release()),
			satisfied: true,
			level:     3,
		},
		{
			name:      "builders without a caller don't check it",
			discovery: claimedBy(testSelfSignedBuilder, testSigner),
			satisfied: true,
			level:     1,
		},
		{
			name:      "a builder with only refs allows any workflow",
			policy:    refsOnlyCallerPolicy,
			discovery: calledAs(releaseOf(otherWorkflow)),
			satisfied: true,
			level:     3,
		},
		{
			name:       "a caller at a branch is rejected",
			discovery:  calledAs(releaseAt("refs/heads/main")),
			violations: []string{refsViolation},
		},
		{
			name:       "a pull request caller is rejected",
			discovery:  calledAs(releaseAt("refs/pull/1/merge")),
			violations: []string{refsViolation},
		},
		{
			name:       "another calling workflow is rejected",
			discovery:  calledAs(releaseOf(otherWorkflow)),
			violations: []string{"the builder allows the workflows " + testCallerWorkflow},
		},
		{
			// The SLSA verifier binds the certificate to the source.
			name:       "a caller in another repository is rejected",
			discovery:  calledAs(foreignRelease()),
			violations: []string{"builder-identity-bound"},
		},
		{
			name: "a caller in another repository is rejected without the source repository of the certificate",
			discovery: calledFrom(
				foreignRelease(), foreignRelease(), "",
			),
			violations: []string{"which is not in a policy source"},
		},
		{
			name: "a certificate whose calling workflow is in another repository than its run is rejected",
			discovery: calledFrom(
				foreignRelease(), foreignRelease(), testRepository,
			),
			violations: []string{"but the repository " + testRepository},
		},
		{
			name:       "provenance that names another caller than its certificate is rejected",
			discovery:  calledBy(release(), releaseAt("refs/tags/v1.3.0")),
			violations: []string{"but its signing certificate"},
		},
		{
			name:       "provenance that names no caller is rejected",
			discovery:  calledBy(nil, release()),
			violations: []string{"it names no calling workflow"},
		},
		{
			name:       "a certificate without a calling workflow is rejected",
			discovery:  calledBy(release(), nil),
			violations: []string{"its signing certificate names no calling workflow"},
		},
		{
			name:      "SLSA v0.2 provenance of an allowed caller verifies at the builder level",
			discovery: v02CalledAs(release()),
			satisfied: true,
			level:     3,
		},
		{
			name:       "SLSA v0.2 provenance of another calling workflow is rejected",
			discovery:  v02CalledAs(releaseOf(otherWorkflow)),
			violations: []string{"the builder allows the workflows " + testCallerWorkflow},
		},
		{
			name:       "SLSA v0.2 provenance of a caller at a branch is rejected",
			discovery:  v02CalledAs(releaseAt("refs/heads/main")),
			violations: []string{refsViolation},
		},
		{
			name:       "SLSA v0.2 provenance that names another caller than its certificate is rejected",
			discovery:  v02CalledBy(release(), releaseAt("refs/tags/v1.3.0")),
			violations: []string{"but its signing certificate"},
		},
		{
			name:      "a source written as a URL matches the calling repository",
			policy:    withSources(testRepository),
			discovery: calledFrom(release(), release(), ""),
			satisfied: true,
			level:     3,
		},
		{
			name:      "a source with a .git suffix matches the calling repository",
			policy:    withSources(testRepository + ".git"),
			discovery: calledFrom(release(), release(), ""),
			satisfied: true,
			level:     3,
		},
		{
			// Only the builder at another release has a caller.
			name:      "provenance of a builder without a caller is not parsed for one",
			policy:    pinnedCallerPolicy,
			discovery: withMalformedCaller,
			satisfied: true,
			level:     3,
		},
		{
			name:       "a calling workflow that is not an object is rejected for a builder with a caller",
			discovery:  withMalformedCaller,
			violations: []string{"parsing its calling workflow"},
		},
		{
			// The level 1 provenance still counts.
			name:       "provenance rejected for its caller leaves the other builders",
			discovery:  claimedByBoth(testSelfSignedBuilder, testGeneratorBuilder+"@"+testReleaseRef),
			satisfied:  true,
			level:      1,
			provenance: bothLocations[0],
		},
	}

	for i := range cases {
		if cases[i].policy == nil {
			cases[i].policy = callerPolicy
		}
	}

	return cases
}

func TestParseBuildConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uri     string
		want    *workflowRun
		wantErr string
	}{
		{
			uri:  testRepository + "/" + testCallerWorkflow + "@" + testReleaseRef,
			want: release(),
		},
		{
			uri:  testRepository + "/.github/workflows/nested/build.yml@refs/heads/main",
			want: &workflowRun{Repository: testRepository, Path: ".github/workflows/nested/build.yml", Ref: "refs/heads/main"},
		},
		{uri: "", wantErr: "is not a GitHub workflow"},
		{uri: "https://gitlab.com/org/repo/.gitlab-ci.yml@refs/heads/main", wantErr: "is not a GitHub workflow"},
		{uri: testRepository + "/" + testCallerWorkflow, wantErr: notWorkflowAtRef},
		{uri: testRepository + "@" + testReleaseRef, wantErr: notWorkflowAtRef},
		{uri: testRepository + "/" + testCallerWorkflow + "@", wantErr: notWorkflowAtRef},
	} {
		t.Run(tc.uri, func(t *testing.T) {
			t.Parallel()

			got, err := parseBuildConfig(tc.uri)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
