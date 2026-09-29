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
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	sapi "github.com/carabiner-dev/signer/api/v1"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/sirupsen/logrus"
	"github.com/slsa-framework/verifier/pkg/slsa"
	"github.com/slsa-framework/verifier/pkg/subject"
)

// VerifierID identifies kpromo as the verifier in SLSA verification
// results.
const VerifierID = "https://github.com/kubernetes-sigs/promo-tools"

// buildProvenanceTypes are the SLSA build provenance predicate types a
// policy accepts.
var buildProvenanceTypes = []string{
	"https://slsa.dev/provenance/v1",
	"https://slsa.dev/provenance/v0.2",
}

// PolicyResult is the outcome of evaluating a policy for one image.
type PolicyResult struct {
	// Satisfied is true when the image satisfies the policy.
	Satisfied bool

	// Provenance is the location of the build provenance that satisfied
	// the policy.
	Provenance string

	// SLSALevel is the SLSA build level the accepted provenance reached.
	SLSALevel int

	// Notice is what the SLSA verifier noted about the accepted
	// provenance, for example that nothing binds its builder to its
	// signer.
	Notice string

	// Violations explain why the policy is not satisfied. It is empty
	// when Satisfied is true.
	Violations []string

	// Accepted are the attestations of the image the policy trusts:
	// their signature verified, they are about the image digest, one of
	// the policy signers signed them and, for build provenance, they
	// passed the policy. They point into the discovery.
	Accepted []*Attestation
}

// ImageProvenance is what the provenance phase concluded for one staging
// image.
type ImageProvenance struct {
	// Policies are the enabled provenance policies that apply to the
	// image, and Results their outcome, in the same order.
	Policies []*Policy
	Results  []*PolicyResult
}

// PolicyEvaluator evaluates provenance policies against the attestations
// found for an image, using the SLSA verifier.
type PolicyEvaluator struct {
	verifier *slsa.Verifier
}

// NewPolicyEvaluator returns an evaluator with the SLSA verifier's
// embedded control catalog and builder registry.
func NewPolicyEvaluator() (*PolicyEvaluator, error) {
	v, err := slsa.New()
	if err != nil {
		return nil, fmt.Errorf("creating SLSA verifier: %w", err)
	}

	return &PolicyEvaluator{verifier: v}, nil
}

// Evaluate checks the attestations in the discovery against the policy.
// Only attestations whose signature verified, that were signed by one of
// the policy signers and whose subjects include the image digest count.
func (e *PolicyEvaluator) Evaluate(
	ctx context.Context, discovery *Discovery, policy *Policy,
) (*PolicyResult, error) {
	if discovery == nil {
		return nil, errors.New("no discovery to evaluate")
	}

	if policy == nil {
		return nil, errors.New("no policy to evaluate")
	}

	signers, err := policy.identities()
	if err != nil {
		return nil, err
	}

	if len(signers) == 0 {
		return nil, errors.New("provenance: at least one signer is required")
	}

	digestRef, err := name.NewDigest(discovery.Reference)
	if err != nil {
		return nil, fmt.Errorf("parsing reference %q: %w", discovery.Reference, err)
	}

	expected, err := subject.Parse(digestRef.DigestStr())
	if err != nil {
		return nil, fmt.Errorf("parsing digest of %s: %w", discovery.Reference, err)
	}

	result := &PolicyResult{}

	var (
		rejected  []string
		satisfied bool
		passed    = map[*Attestation]bool{}
	)

	// Every build provenance is verified, so that only those that pass
	// count as accepted.
	for i := range discovery.Attestations {
		att := &discovery.Attestations[i]
		if !slices.Contains(buildProvenanceTypes, att.PredicateType) {
			continue
		}

		level, notice, err := e.verifyProvenance(ctx, att, signers, expected, policy)
		if err != nil {
			rejected = append(rejected, fmt.Sprintf("%s provenance %s: %v", att.Source, att.Location, err))

			continue
		}

		passed[att] = true

		if !satisfied {
			result.Provenance = att.Location
			result.SLSALevel = level
			result.Notice = notice
			satisfied = true
		}
	}

	if !satisfied {
		if len(rejected) == 0 {
			result.Violations = append(result.Violations, "no SLSA build provenance found")
		}

		result.Violations = append(result.Violations, rejected...)
	}

	// Build provenance counts only when it passed the policy.
	for _, att := range trustedAttestations(discovery, signers, expected) {
		if !slices.Contains(buildProvenanceTypes, att.PredicateType) || passed[att] {
			result.Accepted = append(result.Accepted, att)
		}
	}

	for _, predicateType := range policy.PredicateTypes {
		if !slices.ContainsFunc(result.Accepted, func(att *Attestation) bool {
			return att.PredicateType == predicateType
		}) {
			result.Violations = append(result.Violations, fmt.Sprintf(
				"no attestation of type %s about %s signed by a trusted signer",
				predicateType, expected.Name,
			))
		}
	}

	result.Satisfied = len(result.Violations) == 0

	return result, nil
}

// verifyProvenance verifies one build provenance against the policy and
// returns the SLSA build level it reached and the verifier's notice. It
// passes when it verifies for any of the policy sources.
func (e *PolicyEvaluator) verifyProvenance(
	ctx context.Context,
	att *Attestation,
	signers []*sapi.Identity,
	expected *subject.Expected,
	policy *Policy,
) (int, string, error) {
	if att.Envelope == nil || att.Envelope.GetStatement() == nil {
		return 0, "", errors.New("not a parseable in-toto statement")
	}

	statement := att.Envelope.GetStatement()

	var errs []error

	for _, source := range policy.Sources {
		res, err := e.verifier.Verify(ctx, statement,
			slsa.WithRequireSignatures(true),
			slsa.WithExpectedSigners(signers),
			slsa.WithSubjects([]*subject.Expected{expected}),
			slsa.WithParam("trusted_builders", policy.Builders),
			slsa.WithParam("expected_source", source),
			slsa.WithMinLevel(policy.Level),
			slsa.WithSkipBuildTypeChecks(true),
			slsa.WithVerifierID(VerifierID),
		)
		if err != nil {
			errs = append(errs, err)

			// Signature and identity failures do not depend on the source.
			break
		}

		if res.Pass() {
			return res.SLSALevel, res.Message, nil
		}

		errs = append(errs, fmt.Errorf("source %s: %s", source, failureReason(res)))
	}

	return 0, "", errors.Join(errs...)
}

// failureReason summarizes why a SLSA verification result failed.
func failureReason(res *slsa.Result) string {
	var reasons []string

	for _, layer := range [][]*slsa.ControlResult{res.CoreResults, res.BuildTypeResults, res.UserResults} {
		for _, cr := range layer {
			if cr.Status != slsa.StatusFail && cr.Status != slsa.StatusError {
				continue
			}

			reason := cr.ID
			if cr.Message != "" {
				reason += " (" + cr.Message + ")"
			}

			reasons = append(reasons, reason)
		}
	}

	for _, match := range res.Subjects {
		if !match.Matched {
			reasons = append(reasons, "not about "+match.Expected.Name)
		}
	}

	if res.Message != "" {
		reasons = append(reasons, res.Message)
	}

	if len(reasons) == 0 {
		return string(res.Status)
	}

	return strings.Join(reasons, ", ")
}

// trustedAttestations returns the attestations of the discovery whose
// signature verified, that one of the signers signed and that are about
// the expected subject.
func trustedAttestations(
	discovery *Discovery, signers []*sapi.Identity, expected *subject.Expected,
) []*Attestation {
	var trusted []*Attestation

	for i := range discovery.Attestations {
		att := &discovery.Attestations[i]
		if att.Envelope == nil || att.Envelope.GetStatement() == nil {
			continue
		}

		statement := att.Envelope.GetStatement()

		verification := statement.GetVerification()
		if verification == nil || !verification.GetVerified() {
			continue
		}

		if !slices.ContainsFunc(signers, func(id *sapi.Identity) bool {
			return verification.MatchesIdentity(id)
		}) {
			continue
		}

		matches := subject.MatchAll([]*subject.Expected{expected}, statement.GetSubjects())
		if len(matches) == 1 && matches[0].Matched {
			trusted = append(trusted, att)
		}
	}

	return trusted
}

// sameImage reports whether the discovery is about the image at the
// digest reference.
func sameImage(discovery *Discovery, ref string) bool {
	if discovery == nil {
		return false
	}

	want, err := name.NewDigest(ref)
	if err != nil {
		return false
	}

	got, err := name.NewDigest(discovery.Reference)
	if err != nil {
		return false
	}

	return got.Name() == want.Name()
}

// PolicyChecker checks images against the provenance policy of their
// project.
type PolicyChecker struct {
	once      sync.Once
	evaluator *PolicyEvaluator
	err       error
}

// Check evaluates the policy for the image at the digest reference against
// the attestations discovered for it and returns the result, which is nil
// for a policy that is off. A nil discovery means the discovery failed. A
// violation fails the check in require mode, along with the result, and is
// logged in warn mode. Errors that prevent the evaluation, like an invalid
// policy, fail the check in both modes.
func (c *PolicyChecker) Check(
	ctx context.Context, ref string, policy *Policy, discovery *Discovery,
) (*PolicyResult, error) {
	logrus.Infof("Provenance policy for %s: %s", ref, policy)

	if !policy.Enabled() {
		return nil, nil //nolint:nilnil // a policy that is off has no result
	}

	c.once.Do(func() {
		c.evaluator, c.err = NewPolicyEvaluator()
	})

	if c.err != nil {
		return nil, c.err
	}

	var (
		result *PolicyResult
		err    error
	)

	switch {
	case discovery == nil:
		result = &PolicyResult{Violations: []string{"attestation discovery failed"}}
	case !sameImage(discovery, ref):
		return nil, fmt.Errorf("discovering attestations of %s returned another image", ref)
	default:
		result, err = c.evaluator.Evaluate(ctx, discovery, policy)
		if err != nil {
			return nil, fmt.Errorf("evaluating provenance policy for %s: %w", ref, err)
		}
	}

	if result.Satisfied {
		logrus.Infof(
			"Provenance policy satisfied for %s by %s (SLSA build level %d)",
			ref, result.Provenance, result.SLSALevel,
		)

		if result.Notice != "" {
			logrus.Infof("Provenance of %s: %s", ref, result.Notice)
		}

		return result, nil
	}

	if policy.Mode == PolicyModeRequire {
		return result, fmt.Errorf(
			"provenance policy not satisfied for %s: %s",
			ref, strings.Join(result.Violations, "; "),
		)
	}

	for _, violation := range result.Violations {
		logrus.Warnf("Provenance policy not satisfied for %s: %s", ref, violation)
	}

	return result, nil
}
