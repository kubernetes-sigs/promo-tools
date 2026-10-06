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
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/policylabs/attestation"
	sapi "github.com/policylabs/signer/api/v1"
	"github.com/slsa-framework/verifier/pkg/slsa/eval"
)

// githubURL prefixes the repository and workflow URIs of GitHub Actions
// signing certificates, and workflowsDir holds the workflows of a
// repository.
const (
	githubURL    = "https://github.com/"
	workflowsDir = ".github/workflows/"
)

// Caller constrains the GitHub Actions run that called a builder. The
// signer identity of a reusable workflow is the workflow at the ref its
// caller referenced it with, which any workflow can get by calling it, from
// a branch, a pull request or another repository, with subjects of its
// choice. The constraints are checked against the calling workflow that
// Fulcio records in the signing certificate (its Build Config URI), which
// the provenance has to name as well. The calling workflow always has to be
// in one of the policy sources. When several builders with caller
// constraints match the builder ID of the provenance, it has to satisfy
// all of them.
type Caller struct {
	// Workflows are the paths of the workflows that may call the builder,
	// in the source repository, for example ".github/workflows/build.yml".
	// Empty allows any workflow. They need refs: whoever pushes a ref
	// decides what the workflow file contains at that ref.
	Workflows []string `yaml:"workflows,omitempty"`

	// Refs are patterns of the git ref the calling run ran for, for
	// example "refs/tags/v*". A * matches within one path segment, see
	// path.Match. Empty allows any ref, which only workflows without refs
	// may not do.
	Refs []string `yaml:"refs,omitempty"`

	// Events are the events that may trigger the calling run. The signer
	// library doesn't record the trigger of a signing certificate yet, so
	// they are rejected rather than silently ignored.
	Events []string `yaml:"events,omitempty"`
}

// String describes the caller constraints for the logs, empty without any.
func (c *Caller) String() string {
	if c == nil {
		return ""
	}

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

// validate checks the caller constraints of a builder.
func (c *Caller) validate() error {
	if c == nil {
		return nil
	}

	var errs []error

	if len(c.Events) > 0 {
		errs = append(errs, errors.New(
			"caller events are not supported yet: the signer library doesn't record the trigger of a signing certificate",
		))
	}

	if len(c.Refs) == 0 {
		errs = append(errs, errors.New(
			"caller needs refs: workflows alone only name a file, whose content whoever pushes a ref decides",
		))
	}

	for _, workflow := range c.Workflows {
		if path.Clean(workflow) != workflow || !strings.HasPrefix(workflow, workflowsDir) ||
			strings.Contains(workflow, "@") {
			errs = append(errs, fmt.Errorf(
				"caller workflow %q must be a clean path in %s of the source repository, without a ref",
				workflow, workflowsDir,
			))
		}
	}

	for _, ref := range c.Refs {
		if _, err := path.Match(ref, ""); err != nil || !strings.HasPrefix(ref, "refs/") {
			errs = append(errs, fmt.Errorf("caller ref %q must be a pattern of a full git ref, like refs/tags/v*", ref))
		}
	}

	return errors.Join(errs...)
}

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

// callerProvenance is the part of a build provenance predicate the caller
// checks read: the builder ID and the calling workflow, of SLSA v1 or
// v0.2. The workflow is decoded only for builders with caller constraints,
// so provenance of other builders never fails on it.
type callerProvenance struct {
	BuildDefinition struct {
		ExternalParameters struct {
			// Workflow is the calling workflow GitHub records in SLSA v1.
			Workflow json.RawMessage `json:"workflow"`
		} `json:"externalParameters"`
	} `json:"buildDefinition"`

	RunDetails struct {
		Builder struct {
			ID string `json:"id"`
		} `json:"builder"`
	} `json:"runDetails"`

	Builder struct {
		ID string `json:"id"`
	} `json:"builder"`

	// Invocation names the calling workflow in SLSA v0.2, as the SLSA
	// GitHub generator writes it: the repository at the ref of the run,
	// git+https://github.com/org/repo@refs/tags/v1.2.3, and the path of
	// the workflow as the entry point.
	Invocation struct {
		ConfigSource struct {
			URI        string `json:"uri"`
			EntryPoint string `json:"entryPoint"`
		} `json:"configSource"`
	} `json:"invocation"`
}

// builderID returns the builder ID of SLSA v1 or v0.2 provenance.
func (p *callerProvenance) builderID() string {
	if id := p.RunDetails.Builder.ID; id != "" {
		return id
	}

	return p.Builder.ID
}

// caller returns the calling workflow the provenance names, nil if it
// names none.
func (p *callerProvenance) caller() (*workflowRun, error) {
	if raw := p.BuildDefinition.ExternalParameters.Workflow; len(raw) > 0 && string(raw) != "null" {
		workflow := &workflowRun{}
		if err := json.Unmarshal(raw, workflow); err != nil {
			return nil, fmt.Errorf("parsing its calling workflow: %w", err)
		}

		return workflow, nil
	}

	source := p.Invocation.ConfigSource
	if source.URI == "" && source.EntryPoint == "" {
		return nil, nil //nolint:nilnil // provenance without a caller
	}

	repository, ref, _ := strings.Cut(strings.TrimPrefix(source.URI, "git+"), "@")
	if repository == "" || ref == "" || source.EntryPoint == "" {
		return nil, fmt.Errorf("its config source %q with the entry point %q names no workflow at a ref",
			source.URI, source.EntryPoint)
	}

	return &workflowRun{Repository: repository, Path: source.EntryPoint, Ref: ref}, nil
}

// checkCallers checks the provenance against the caller constraints of the
// builders it claims, out of those its signer may claim. The verifier
// already accepted its builder ID, so at least one of them matches.
func checkCallers(statement attestation.Statement, builders []Builder, sources []string) error {
	if !slices.ContainsFunc(builders, func(b Builder) bool { return b.Caller != nil }) {
		return nil
	}

	predicate := &callerProvenance{}
	if err := json.Unmarshal(statement.GetPredicate().GetData(), predicate); err != nil {
		return fmt.Errorf("parsing the provenance for its caller: %w", err)
	}

	builderID := predicate.builderID()

	var constrained []*Caller

	for i := range builders {
		if builders[i].Caller != nil && (builders[i].ID == builderID || coversBuilder(builders[i].ID, builderID)) {
			constrained = append(constrained, builders[i].Caller)
		}
	}

	if len(constrained) == 0 {
		return nil
	}

	workflow, err := predicate.caller()
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
// the provenance and the caller constraints.
func checkCaller(identity *sapi.IdentitySigstore, workflow *workflowRun, callers []*Caller, sources []string) error {
	run, err := parseBuildConfig(identity.GetBuildConfigUri())
	if err != nil {
		return fmt.Errorf("its signing certificate names no calling workflow: %w", err)
	}

	if repository := identity.GetSourceRepositoryUri(); repository != "" && repository != run.Repository {
		return fmt.Errorf("its signing certificate names the calling workflow %s, but the repository %s", run, repository)
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

	for _, caller := range callers {
		if len(caller.Workflows) > 0 && !slices.Contains(caller.Workflows, run.Path) {
			return fmt.Errorf("it was called by %s, the builder allows the workflows %s",
				run, strings.Join(caller.Workflows, ", "))
		}

		if len(caller.Refs) > 0 && !slices.ContainsFunc(caller.Refs, func(pattern string) bool {
			matched, err := path.Match(pattern, run.Ref)

			return err == nil && matched
		}) {
			return fmt.Errorf("it was called by %s, the builder allows the refs %s",
				run, strings.Join(caller.Refs, ", "))
		}
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
