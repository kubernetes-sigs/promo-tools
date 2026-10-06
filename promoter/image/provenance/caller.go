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
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	provenancev02 "github.com/in-toto/attestation/go/predicates/provenance/v02"
	provenancev1 "github.com/in-toto/attestation/go/predicates/provenance/v1"
	"github.com/policylabs/attestation"
	sapi "github.com/policylabs/signer/api/v1"
	"github.com/slsa-framework/verifier/pkg/slsa/eval"
	"google.golang.org/protobuf/types/known/structpb"
)

// githubURL prefixes the repository and workflow URIs of GitHub Actions
// signing certificates, and workflowsDir holds the workflows of a
// repository.
const (
	githubURL    = "https://github.com/"
	workflowsDir = ".github/workflows/"
)

// Caller is a GitHub Actions run that may call a builder. The signer
// identity of a reusable workflow is the workflow at the ref its caller
// referenced it with, which any workflow can get by calling it, from a
// branch, a pull request or another repository, with subjects of its
// choice. A builder lists the callers it allows as alternatives, and a run
// has to match all fields of one of them. They are checked against the
// calling workflow that Fulcio records in the signing certificate (its
// Build Config URI), which the provenance has to name as well. The calling
// workflow always has to be in one of the policy sources. When several
// builders with callers match the builder ID of the provenance, it has to
// match one caller of each.
type Caller struct {
	// Workflows are the paths of the workflows that may call the builder,
	// in the source repository, for example ".github/workflows/build.yml".
	// Empty allows any workflow. They need refs: whoever pushes a ref
	// decides what the workflow file contains at that ref.
	Workflows []string `yaml:"workflows,omitempty"`

	// Refs are patterns of the git ref the calling run ran for, for
	// example "refs/tags/v*". A * matches within one path segment, see
	// path.Match. They are required.
	Refs []string `yaml:"refs,omitempty"`

	// Events are the events that may trigger the calling run, for example
	// "release" or "push", which Fulcio records as the build trigger of the
	// signing certificate. Empty allows any event.
	Events []string `yaml:"events,omitempty"`
}

// String describes the caller for the logs.
func (c *Caller) String() string {
	var parts []string

	if len(c.Workflows) > 0 {
		parts = append(parts, "workflows ["+strings.Join(slices.Sorted(slices.Values(c.Workflows)), ", ")+"]")
	}

	if len(c.Refs) > 0 {
		parts = append(parts, "refs ["+strings.Join(slices.Sorted(slices.Values(c.Refs)), ", ")+"]")
	}

	if len(c.Events) > 0 {
		parts = append(parts, "events ["+strings.Join(slices.Sorted(slices.Values(c.Events)), ", ")+"]")
	}

	return strings.Join(parts, " ")
}

// callersString describes alternative callers for the logs, in any order.
func callersString(callers []Caller) string {
	strs := make([]string, 0, len(callers))
	for i := range callers {
		strs = append(strs, callers[i].String())
	}

	slices.Sort(strs)

	return "[" + strings.Join(strs, " or ") + "]"
}

// validateCallers checks the callers of a builder, naming each by its
// index.
func validateCallers(callers []Caller) []error {
	var errs []error

	for i := range callers {
		for _, err := range callers[i].validate() {
			errs = append(errs, fmt.Errorf("callers[%d]: %w", i, err))
		}
	}

	return errs
}

// validate checks a caller.
func (c *Caller) validate() []error {
	var errs []error

	for _, event := range c.Events {
		if !eventPattern.MatchString(event) {
			errs = append(errs, fmt.Errorf("event %q must be a GitHub Actions event name, like release", event))
		}
	}

	if len(c.Refs) == 0 {
		errs = append(errs, errors.New(
			"refs are required: workflows alone only name a file, whose content whoever pushes a ref decides",
		))
	}

	for _, workflow := range c.Workflows {
		if path.Clean(workflow) != workflow || !strings.HasPrefix(workflow, workflowsDir) ||
			strings.Contains(workflow, "@") {
			errs = append(errs, fmt.Errorf(
				"workflow %q must be a clean path in %s of the source repository, without a ref",
				workflow, workflowsDir,
			))
		}
	}

	for _, ref := range c.Refs {
		if _, err := path.Match(ref, ""); err != nil || !strings.HasPrefix(ref, "refs/") {
			errs = append(errs, fmt.Errorf("ref %q must be a pattern of a full git ref, like refs/tags/v*", ref))
		}
	}

	return errs
}

// matches reports whether the calling run, triggered by the event, meets
// every field of the caller. Without a recorded event, a caller with
// events can't be checked, so it doesn't match.
func (c *Caller) matches(run *workflowRun, event string) bool {
	return (len(c.Workflows) == 0 || slices.Contains(c.Workflows, run.Path)) &&
		slices.ContainsFunc(c.Refs, func(pattern string) bool {
			matched, err := path.Match(pattern, run.Ref)

			return err == nil && matched
		}) &&
		(len(c.Events) == 0 || (event != "" && slices.Contains(c.Events, event)))
}

// eventPattern matches the names of GitHub Actions events, like release or
// workflow_dispatch.
var eventPattern = regexp.MustCompile(`^[a-z][a-z_]*$`)

// workflowRun is a GitHub Actions workflow at the ref a run ran for.
type workflowRun struct {
	// Repository is the URL of the repository of the workflow, for example
	// https://github.com/kubernetes-sigs/security-profiles-operator.
	Repository string `json:"repository"`

	// Path is the path of the workflow in the repository.
	Path string `json:"path"`

	// Ref is the git ref the run ran for.
	Ref string `json:"ref"`
}

// String returns the workflow as GitHub writes it in certificates.
func (w *workflowRun) String() string {
	return fmt.Sprintf("%s/%s@%s", w.Repository, w.Path, w.Ref)
}

// parseBuildConfig parses the Build Config URI of a GitHub Actions signing
// certificate, which names the top level workflow of the run, for example
// https://github.com/org/repo/.github/workflows/build.yml@refs/tags/v1.2.3.
func parseBuildConfig(uri string) (*workflowRun, error) {
	rest, ok := strings.CutPrefix(uri, githubURL)
	if !ok {
		return nil, fmt.Errorf("%q is not a GitHub workflow", uri)
	}

	location, ref, _ := strings.Cut(rest, "@")

	parts := strings.SplitN(location, "/", 3)
	if len(parts) != 3 || slices.Contains(parts, "") || ref == "" {
		return nil, fmt.Errorf("%q is not a GitHub workflow at a ref", uri)
	}

	return &workflowRun{Repository: githubURL + parts[0] + "/" + parts[1], Path: parts[2], Ref: ref}, nil
}

// provenanceBuilderID returns the builder ID of SLSA v1 or v0.2 provenance
// as the verifier parsed it, empty for other predicates.
func provenanceBuilderID(predicate any) string {
	switch p := predicate.(type) {
	case *provenancev1.Provenance:
		return p.GetRunDetails().GetBuilder().GetId()
	case *provenancev02.Provenance:
		return p.GetBuilder().GetId()
	default:
		return ""
	}
}

// callingWorkflow returns the calling workflow SLSA v1 or v0.2 provenance
// names, nil if it names none. GitHub records it in the external parameters
// in SLSA v1. The SLSA GitHub generator records it as the config source in
// SLSA v0.2: the repository at the ref of the run,
// git+https://github.com/org/repo@refs/tags/v1.2.3, and the path of the
// workflow as the entry point.
func callingWorkflow(predicate any) (*workflowRun, error) {
	switch p := predicate.(type) {
	case *provenancev1.Provenance:
		value := p.GetBuildDefinition().GetExternalParameters().GetFields()["workflow"]
		if _, null := value.GetKind().(*structpb.Value_NullValue); value == nil || null {
			return nil, nil //nolint:nilnil // provenance without a caller
		}

		workflow := value.GetStructValue()
		if workflow == nil {
			return nil, errors.New("its calling workflow is not an object")
		}

		fields := workflow.GetFields()

		return &workflowRun{
			Repository: fields["repository"].GetStringValue(),
			Path:       fields["path"].GetStringValue(),
			Ref:        fields["ref"].GetStringValue(),
		}, nil
	case *provenancev02.Provenance:
		source := p.GetInvocation().GetConfigSource()
		if source.GetUri() == "" && source.GetEntryPoint() == "" {
			return nil, nil //nolint:nilnil // provenance without a caller
		}

		repository, ref, _ := strings.Cut(strings.TrimPrefix(source.GetUri(), "git+"), "@")
		if repository == "" || ref == "" || source.GetEntryPoint() == "" {
			return nil, fmt.Errorf("its config source %q with the entry point %q names no workflow at a ref",
				source.GetUri(), source.GetEntryPoint())
		}

		return &workflowRun{Repository: repository, Path: source.GetEntryPoint(), Ref: ref}, nil
	default:
		return nil, nil //nolint:nilnil // not provenance that names a caller
	}
}

// checkCallers checks the provenance against the callers of the builders
// it claims, out of those its signer may claim. The verifier already
// accepted its builder ID, so at least one of them matches.
func checkCallers(statement attestation.Statement, builders []Builder, sources []string) error {
	if !slices.ContainsFunc(builders, func(b Builder) bool { return len(b.Callers) > 0 }) {
		return nil
	}

	// The predicate the verifier parsed holds the builder ID it accepted.
	var predicate any
	if p := statement.GetPredicate(); p != nil {
		predicate = p.GetParsed()
	}

	builderID := provenanceBuilderID(predicate)
	if builderID == "" {
		return errors.New("its caller can't be checked: it is no parsed SLSA v1 or v0.2 provenance with a builder ID")
	}

	// The callers of every builder that matches, each a list of
	// alternatives.
	var constrained [][]Caller

	for i := range builders {
		if len(builders[i].Callers) > 0 && (builders[i].ID == builderID || coversBuilder(builders[i].ID, builderID)) {
			constrained = append(constrained, builders[i].Callers)
		}
	}

	if len(constrained) == 0 {
		return nil
	}

	workflow, err := callingWorkflow(predicate)
	if err != nil {
		return fmt.Errorf("caller of builder %s: %w", builderID, err)
	}

	identities := signingIdentities(statement.GetVerification())
	if len(identities) == 0 {
		return fmt.Errorf("caller of builder %s: its signature names no sigstore identity to check", builderID)
	}

	// A signature has one certificate, so this is the error of its
	// identity.
	var first error

	for _, identity := range identities {
		err := checkCaller(identity, workflow, constrained, sources)
		if err == nil {
			return nil
		}

		if first == nil {
			first = err
		}
	}

	return fmt.Errorf("caller of builder %s: %w", builderID, first)
}

// checkCaller checks the calling workflow of one signing identity against
// the provenance and the callers of each builder, of which it has to match
// one.
func checkCaller(identity *sapi.IdentitySigstore, workflow *workflowRun, constrained [][]Caller, sources []string) error {
	run, err := parseBuildConfig(identity.GetBuildConfigUri())
	if err != nil {
		return fmt.Errorf("its signing certificate names no calling workflow: %w", err)
	}

	if repository := identity.GetSourceRepositoryUri(); repository != "" && repository != run.Repository {
		return fmt.Errorf("its signing certificate names the calling workflow %s, but the repository %s", run, repository)
	}

	if ref := identity.GetSourceRepositoryRef(); ref != "" && ref != run.Ref {
		return fmt.Errorf("its signing certificate names the calling workflow %s, but the ref %s", run, ref)
	}

	if !inSources(sources, run.Repository) {
		return fmt.Errorf("it was called by %s, which is not in a policy source", run)
	}

	if workflow == nil {
		return errors.New("it names no calling workflow, which its signing certificate names as " + run.String())
	}

	if *workflow != *run {
		return fmt.Errorf("it names the calling workflow %s, but its signing certificate %s", workflow, run)
	}

	trigger := identity.GetBuildTrigger()

	for _, callers := range constrained {
		if slices.ContainsFunc(callers, func(c Caller) bool { return c.matches(run, trigger) }) {
			continue
		}

		event := "the " + trigger + " event"
		if trigger == "" {
			event = "an event its signing certificate doesn't record"
		}

		return fmt.Errorf("it was called by %s for %s, which matches none of the allowed callers %s",
			run, event, callersString(callers))
	}

	return nil
}

// inSources reports whether the repository is one of the policy sources,
// compared like the SLSA verifier compares sources.
func inSources(sources []string, repository string) bool {
	return slices.ContainsFunc(sources, func(source string) bool {
		matched, err := eval.RepoMatches(source, repository)

		return err == nil && matched
	})
}

// signingIdentities returns the sigstore identities of a verified
// signature.
func signingIdentities(verification attestation.Verification) []*sapi.IdentitySigstore {
	v, ok := verification.(interface {
		GetSignature() *sapi.SignatureVerification
	})
	if !ok || !verification.GetVerified() {
		return nil
	}

	var identities []*sapi.IdentitySigstore

	for _, id := range v.GetSignature().GetIdentities() {
		if sigstore := id.GetSigstore(); sigstore != nil {
			identities = append(identities, sigstore)
		}
	}

	return identities
}
