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

	sapi "github.com/policylabs/signer/api/v1"
	"github.com/stretchr/testify/require"
)

const (
	// testCallerWorkflow is the release workflow that calls the provenance
	// generator, and testRepository the repository of both.
	testCallerWorkflow = ".github/workflows/build.yml"
	testRepository     = "https://" + testSource
	testReleaseRef     = "refs/tags/v1.2.0"

	// testReleaseRefs is the ref pattern of release tags, and mainRef the
	// ref of the main branch.
	testReleaseRefs = "refs/tags/v*"
	mainRef         = "refs/heads/main"

	// releaseEvent is the event of published releases, and pushEvent the
	// event of pushes.
	releaseEvent = "release"
	pushEvent    = "push"

	// callersViolation is the violation of a calling run that matches none
	// of the callers of its builder, and unrecordedEvent describes the event
	// of a run whose certificate records none.
	callersViolation = "which matches none of the allowed callers"
	unrecordedEvent  = "for an event its signing certificate doesn't record"

	// uriField and digestField name the URI and the digest of a resource
	// descriptor.
	uriField    = "uri"
	digestField = "digest"
)

// testCallers allow the release workflow to call the generator for
// release tags.
func testCallers() []Caller {
	return []Caller{{Workflows: []string{testCallerWorkflow}, Refs: []string{testReleaseRefs}}}
}

// eventsCallerPolicy returns callerPolicy that allows the release event
// only.
func eventsCallerPolicy() *Policy {
	p := callerPolicy()
	p.Builders[1].Callers[0].Events = []string{releaseEvent}

	return p
}

// refsOnlyCallerPolicy returns callerPolicy without the workflows.
func refsOnlyCallerPolicy() *Policy {
	p := callerPolicy()
	p.Builders[1].Callers[0].Workflows = nil

	return p
}

// callerPolicy returns boundBuilderPolicy with testCallers on the
// provenance generator.
func callerPolicy() *Policy {
	p := boundBuilderPolicy(0)
	p.Builders[1].Callers = testCallers()

	return p
}

// alternativeCallersPolicy returns callerPolicy whose generator allows,
// like the provenance workflow of the Security Profiles Operator, releases
// of release tags from the release workflow or another one, or pushes to
// the main branch from the release workflow.
func alternativeCallersPolicy() *Policy {
	p := callerPolicy()
	p.Builders[1].Callers = []Caller{
		{
			Workflows: []string{testCallerWorkflow, otherWorkflow},
			Refs:      []string{testReleaseRefs},
			Events:    []string{releaseEvent},
		},
		{Workflows: []string{testCallerWorkflow}, Refs: []string{mainRef}, Events: []string{pushEvent}},
	}

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
// which is left out when empty, and further certificate fields that set
// fills in.
func calledFrom(
	workflow, run *workflowRun, repository string, set ...func(*sapi.IdentitySigstore),
) func(t *testing.T) *Discovery {
	return func(t *testing.T) *Discovery {
		t.Helper()

		verification := signed(t, testGeneratorSigner)

		if run != nil {
			identity := verification.GetSignature().GetIdentities()[0].GetSigstore()
			identity.BuildConfigUri = run.String()
			identity.SourceRepositoryUri = repository

			for _, f := range set {
				f(identity)
			}
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

// withProtoFieldNames returns calledAs for the run, with provenance that
// spells its run details by their proto field name, which the verifier
// accepts as well.
func withProtoFieldNames(run *workflowRun) func(t *testing.T) *Discovery {
	return func(t *testing.T) *Discovery {
		t.Helper()

		var statement map[string]any
		require.NoError(t, json.Unmarshal(provenanceStatement(t, provenanceOptions{
			builder:  testGeneratorBuilder + "@" + testReleaseRef,
			workflow: run,
		}), &statement))

		predicate, ok := statement["predicate"].(map[string]any)
		require.True(t, ok)

		predicate["run_details"] = predicate["runDetails"]
		delete(predicate, "runDetails")

		data, err := json.Marshal(statement)
		require.NoError(t, err)

		verification := signed(t, testGeneratorSigner)
		identity := verification.GetSignature().GetIdentities()[0].GetSigstore()
		identity.BuildConfigUri = run.String()
		identity.SourceRepositoryUri = run.Repository

		return newDiscovery(newAttestation(t, data, verification))
	}
}

// withSources returns callerPolicy with other spellings of its source.
func withSources(sources ...string) func() *Policy {
	return func() *Policy {
		p := callerPolicy()
		p.Sources = sources

		return p
	}
}

// pinnedCallerPolicy returns boundBuilderPolicy with callers on the
// generator at another release, next to the generator at any ref without
// them.
func pinnedCallerPolicy() *Policy {
	p := boundBuilderPolicy(0)
	p.Builders = append(p.Builders, Builder{
		ID: testGeneratorBuilder + "@refs/tags/v0.9.0", Level: 3,
		Signers: []string{testGeneratorSigner}, Callers: testCallers(),
	})

	return p
}

// releaseEventsCallerPolicy returns callerPolicy with the generator at the
// release ref as well, whose caller allows the release event only.
func releaseEventsCallerPolicy() *Policy {
	p := callerPolicy()
	p.Builders = append(p.Builders, Builder{
		ID: testGeneratorBuilder + "@" + testReleaseRef, Level: 3,
		Signers: []string{testGeneratorSigner},
		Callers: []Caller{{Refs: []string{testReleaseRefs}, Events: []string{releaseEvent}}},
	})

	return p
}

// withRun returns the release run, changed by the function.
func withRun(change func(*workflowRun)) *workflowRun {
	run := release()
	change(run)

	return run
}

// releaseAt returns the release run at another ref, otherRelease the
// release run of another workflow, and foreignRelease the release run in
// another repository.
func releaseAt(ref string) *workflowRun {
	return withRun(func(r *workflowRun) { r.Ref = ref })
}

func otherRelease() *workflowRun {
	return withRun(func(r *workflowRun) { r.Path = otherWorkflow })
}

func foreignRelease() *workflowRun {
	return withRun(func(r *workflowRun) { r.Repository = otherRepositoryURL })
}

// triggeredBy returns runFor the release run.
func triggeredBy(event string) func(t *testing.T) *Discovery {
	return runFor(release(), event)
}

// runFor returns calledAs for the run, whose certificate records the event
// as its build trigger and the ref of the run as its source ref. An empty
// event leaves the build trigger out.
func runFor(run *workflowRun, event string) func(t *testing.T) *Discovery {
	return calledFrom(run, run, run.Repository, func(identity *sapi.IdentitySigstore) {
		identity.BuildTrigger = event
		identity.SourceRepositoryRef = run.Ref
	})
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

// callerTestCases cover the callers of builders, with
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
			name:      "a caller triggered by an allowed event verifies at the builder level",
			policy:    eventsCallerPolicy,
			discovery: triggeredBy(releaseEvent),
			satisfied: true,
			level:     3,
		},
		{
			name:       "a caller triggered by another event is rejected",
			policy:     eventsCallerPolicy,
			discovery:  triggeredBy("workflow_dispatch"),
			violations: []string{"for the workflow_dispatch event, " + callersViolation},
		},
		{
			name:       "a certificate without a build trigger is rejected when the builder allows events",
			policy:     eventsCallerPolicy,
			discovery:  triggeredBy(""),
			violations: []string{unrecordedEvent + ", " + callersViolation},
		},
		{
			name:      "a caller without events allows any trigger",
			discovery: triggeredBy("workflow_dispatch"),
			satisfied: true,
			level:     3,
		},
		{
			name: "a certificate whose source ref is not the ref of its calling workflow is rejected",
			discovery: calledFrom(release(), release(), testRepository, func(identity *sapi.IdentitySigstore) {
				identity.SourceRepositoryRef = mainRef
			}),
			violations: []string{"but the ref " + mainRef},
		},
		{
			name:      "builders without callers don't check the caller",
			discovery: claimedBy(testSelfSignedBuilder, testSigner),
			satisfied: true,
			level:     1,
		},
		{
			name:      "a caller with only refs allows any workflow",
			policy:    refsOnlyCallerPolicy,
			discovery: calledAs(otherRelease()),
			satisfied: true,
			level:     3,
		},
		{
			name:       "a caller at a branch is rejected",
			discovery:  calledAs(releaseAt(mainRef)),
			violations: []string{releaseAt(mainRef).String() + " " + unrecordedEvent + ", " + callersViolation},
		},
		{
			name:       "a pull request caller is rejected",
			discovery:  calledAs(releaseAt("refs/pull/1/merge")),
			violations: []string{callersViolation},
		},
		{
			name:      "another calling workflow is rejected",
			discovery: calledAs(otherRelease()),
			violations: []string{
				"called by " + otherRelease().String(),
				callersViolation + " [workflows [" + testCallerWorkflow + "] refs [" + testReleaseRefs + "]]",
			},
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
			discovery:  v02CalledAs(otherRelease()),
			violations: []string{callersViolation},
		},
		{
			name:       "SLSA v0.2 provenance of a caller at a branch is rejected",
			discovery:  v02CalledAs(releaseAt(mainRef)),
			violations: []string{callersViolation},
		},
		{
			name:       "SLSA v0.2 provenance that names another caller than its certificate is rejected",
			discovery:  v02CalledBy(release(), releaseAt("refs/tags/v1.3.0")),
			violations: []string{"but its signing certificate"},
		},
		{
			name:       "SLSA v0.2 provenance whose config source has no ref is rejected",
			discovery:  v02CalledBy(releaseAt(""), release()),
			violations: []string{"names no workflow at a ref"},
		},
		{
			name:       "provenance that spells its fields like the proto is checked for its caller",
			discovery:  withProtoFieldNames(releaseAt(mainRef)),
			violations: []string{callersViolation},
		},
		{
			name:      "provenance matching a caller of all builders with callers that match it verifies",
			policy:    releaseEventsCallerPolicy,
			discovery: triggeredBy(releaseEvent),
			satisfied: true,
			level:     3,
		},
		{
			name:       "provenance has to match a caller of every builder with callers that matches it",
			policy:     releaseEventsCallerPolicy,
			discovery:  triggeredBy(pushEvent),
			violations: []string{"for the push event, " + callersViolation + " [refs [" + testReleaseRefs + "] events [release]]"},
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
			name:      "provenance of a builder without callers is not parsed for a caller",
			policy:    pinnedCallerPolicy,
			discovery: withMalformedCaller,
			satisfied: true,
			level:     3,
		},
		{
			name:       "a calling workflow that is not an object is rejected for a builder with callers",
			discovery:  withMalformedCaller,
			violations: []string{"its calling workflow is not an object"},
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

// alternativeCallerTestCases cover builders with alternative callers, with
// alternativeCallersPolicy.
func alternativeCallerTestCases() []evaluateTestCase {
	cases := []evaluateTestCase{
		{
			name:      "a run matching the first of alternative callers verifies",
			discovery: triggeredBy(releaseEvent),
			satisfied: true,
			level:     3,
		},
		{
			name:      "a run matching another workflow of the first of alternative callers verifies",
			discovery: runFor(otherRelease(), releaseEvent),
			satisfied: true,
			level:     3,
		},
		{
			name:      "a run matching the second of alternative callers verifies",
			discovery: runFor(releaseAt(mainRef), pushEvent),
			satisfied: true,
			level:     3,
		},
		{
			// The workflow and the ref are allowed by the first caller, the
			// workflow and the event by the second.
			name:       "a run matching the fields of different callers is rejected",
			discovery:  triggeredBy(pushEvent),
			violations: []string{"for the push event, " + callersViolation},
		},
		{
			// The ref and the event are allowed by the second caller, the
			// workflow by the first.
			name:       "a run of a workflow only another caller allows is rejected",
			discovery:  runFor(withRun(func(r *workflowRun) { r.Path, r.Ref = otherWorkflow, mainRef }), pushEvent),
			violations: []string{"for the push event, " + callersViolation},
		},
		{
			name:       "a run without a build trigger matches no alternative caller with events",
			discovery:  runFor(release(), ""),
			violations: []string{unrecordedEvent + ", " + callersViolation},
		},
	}

	for i := range cases {
		cases[i].policy = alternativeCallersPolicy
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
