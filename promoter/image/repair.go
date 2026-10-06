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

package imagepromoter

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/sirupsen/logrus"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
)

// repairAttestations carries the accepted staging attestations and writes
// the verification summaries that promoted images of manifests with an
// enabled provenance policy miss, for example because the attest phase of
// the run that promoted them failed. Those images are evaluated against
// their policies again. They are promoted already, so a policy they don't
// satisfy blocks nothing; their summaries record it.
func (p *Promoter) repairAttestations(
	ctx context.Context, opts *options.Options, mfests []schema.Manifest, promotionEdges map[promotion.Edge]any,
) error {
	if !opts.SignImages {
		logrus.Info("Not repairing attestations (--sign=false)")

		return nil
	}

	candidates, err := repairCandidates(mfests, promotionEdges)
	if err != nil {
		return err
	}

	if len(candidates) == 0 {
		logrus.Info("No promoted images with a provenance policy to repair")

		return nil
	}

	repairs, findErr := p.impl.FindAttestationRepairs(ctx, opts, candidates)
	if findErr != nil {
		findErr = fmt.Errorf("finding attestations to repair: %w", findErr)
	}

	if len(repairs) == 0 {
		if findErr == nil {
			logrus.Info("No attestations to repair")
		}

		return findErr
	}

	// A staging image promoted under another tag in this run was
	// discovered already.
	discoveries := map[string]*provenance.Discovery{}
	undiscovered := map[promotion.Edge]any{}

	for edge := range repairs {
		if discovery, ok := p.discoveries[edge.SrcReference()]; ok {
			discoveries[edge.SrcReference()] = discovery
		} else {
			undiscovered[edge] = nil
		}
	}

	if len(undiscovered) > 0 {
		maps.Copy(discoveries, p.discoverStagingImages(ctx, undiscovered))
	}

	outcomes, err := evaluateRepairs(ctx, mfests, repairs, discoveries)
	if err != nil {
		return errors.Join(findErr, err)
	}

	p.addRepairResults(discoveries, outcomes)

	errs := []error{findErr}

	if err := p.impl.CarryAttestations(ctx, opts, repairs, outcomes); err != nil {
		errs = append(errs, fmt.Errorf("repairing carried attestations: %w", err))
	}

	if err := p.impl.WriteVerificationSummaries(ctx, opts, mfests, repairs, discoveries, outcomes); err != nil {
		errs = append(errs, fmt.Errorf("repairing verification summaries: %w", err))
	}

	return errors.Join(errs...)
}

// addRepairResults adds the discoveries and outcomes of the images
// evaluated again for a repair to those of the run. The provenance phase
// result of an image promoted in this run is kept: it is what decided its
// promotion.
func (p *Promoter) addRepairResults(
	discoveries map[string]*provenance.Discovery, outcomes map[string]*provenance.ImageProvenance,
) {
	if p.discoveries == nil {
		p.discoveries = make(map[string]*provenance.Discovery, len(discoveries))
	}

	if p.provenance == nil {
		p.provenance = make(map[string]*provenance.ImageProvenance, len(outcomes))
	}

	for ref, discovery := range discoveries {
		if _, ok := p.discoveries[ref]; !ok {
			p.discoveries[ref] = discovery
		}
	}

	for ref, outcome := range outcomes {
		if _, ok := p.provenance[ref]; !ok {
			p.provenance[ref] = outcome
		}
	}
}

// repairCandidates returns the edges of the manifests that have an enabled
// provenance policy and are no promotion candidates of this run, so most
// likely promoted already.
func repairCandidates(
	mfests []schema.Manifest, promotionEdges map[promotion.Edge]any,
) (map[promotion.Edge]any, error) {
	policies, err := schema.ProvenancePolicies(mfests)
	if err != nil {
		return nil, fmt.Errorf("reading provenance policies: %w", err)
	}

	edges, err := promotion.ToEdges(mfests)
	if err != nil {
		return nil, fmt.Errorf("converting manifests to edges: %w", err)
	}

	candidates := map[promotion.Edge]any{}

	for edge := range edges {
		if _, promoting := promotionEdges[edge]; promoting {
			continue
		}

		if len(schema.ApplicableProvenancePolicies(policies, edge.SrcRegistry.Name, edge.SrcImageTag.Name)) > 0 {
			candidates[edge] = nil
		}
	}

	return candidates, nil
}

// evaluateRepairs evaluates the provenance policies of the images to
// repair, by source reference. Violations are logged. An image whose
// attestations could not be discovered, for example because it is gone
// from staging, or whose evaluation failed has no result, so that neither
// attestations nor a summary are written for it.
func evaluateRepairs(
	ctx context.Context,
	mfests []schema.Manifest,
	edges map[promotion.Edge]any,
	discoveries map[string]*provenance.Discovery,
) (map[string]*provenance.ImageProvenance, error) {
	refPolicies, err := sourcePolicies(mfests, edges)
	if err != nil {
		return nil, err
	}

	checker := &provenance.PolicyChecker{}
	outcomes := make(map[string]*provenance.ImageProvenance, len(refPolicies))

	for _, ref := range slices.Sorted(maps.Keys(refPolicies)) {
		if discoveries[ref] == nil {
			logrus.Warnf("Not repairing the attestations of %s: they were not discovered", ref)

			continue
		}

		outcome := &provenance.ImageProvenance{}
		evaluated := true

		for _, applied := range refPolicies[ref] {
			result, err := checker.Check(ctx, ref, applied.Policy, applied.Image, discoveries[ref])
			if result == nil {
				if err != nil {
					logrus.Warnf("Not repairing the attestations of %s: %v", ref, err)

					evaluated = false

					break
				}

				continue
			}

			if err != nil {
				logrus.Warnf("Promoted image %s: %v", ref, err)
			}

			outcome.Policies = append(outcome.Policies, applied.Policy)
			outcome.Results = append(outcome.Results, result)
		}

		if evaluated {
			outcomes[ref] = outcome
		}
	}

	return outcomes, nil
}
