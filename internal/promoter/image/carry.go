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
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
)

// emptyConfigDigest is the digest of the empty JSON config of OCI artifacts.
const emptyConfigDigest = "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"

// promoterPredicateTypes are the predicate types of the attestations the
// promoter writes itself, which are never carried from staging: promotion
// records and verification summaries.
var promoterPredicateTypes = []string{
	provenance.PredicateType,
	"https://slsa.dev/verification_summary/v1",
}

// CarryAttestations copies the staging attestations accepted by the
// provenance policies of an image to the canonical registry, where the
// promotion record and the signatures of the image live. They are
// referrers of the image digest, which promotion keeps, so they are copied
// digest-identical: signatures and subjects stay valid.
//
// An attestation is carried when every enabled policy that applies to the
// image is satisfied and accepted it. Images without an enabled policy
// carry nothing, and neither do attestations in legacy `.att` tags. Like
// the promotion records, nothing is carried without signing.
func (di *DefaultPromoterImplementation) CarryAttestations(
	ctx context.Context,
	opts *options.Options,
	edges map[promotion.Edge]any,
	results map[string]*provenance.ImageProvenance,
) error {
	if !opts.SignImages || len(edges) == 0 || len(results) == 0 {
		return nil
	}

	// One copy per target identity and digest: the canonical registry
	// holds the attestations for every region and tag of a digest.
	grouped := groupEdgesByIdentityDigest(edges, withTagless)

	// Promoted images are no promotion candidates in later runs, so one
	// failure must not keep the others from being carried.
	var (
		g    errgroup.Group
		mu   sync.Mutex
		errs []error
	)

	g.SetLimit(opts.MaxSignatureOps)

	for _, group := range grouped {
		// The same digest can come from several staging repositories;
		// each carries what its own policies accepted.
		sources := map[string]promotion.Edge{}
		for i := range group {
			if _, ok := sources[group[i].SrcReference()]; !ok {
				sources[group[i].SrcReference()] = group[i]
			}
		}

		var carried []sourceAttestations

		for _, ref := range slices.Sorted(maps.Keys(sources)) {
			edge := sources[ref]
			if atts := carriedAttestations(results[ref], string(edge.Digest)); len(atts) > 0 {
				carried = append(carried, sourceAttestations{edge: edge, attestations: atts})
			}
		}

		if len(carried) == 0 {
			continue
		}

		// The attestations of a digest are copied one after another, from
		// all its sources. On registries without the referrers API, each
		// copy updates the same referrers tag, and parallel updates would
		// lose entries.
		g.Go(func() error {
			if err := di.carryDigestAttestations(ctx, carried); err != nil {
				mu.Lock()
				defer mu.Unlock()

				errs = append(errs, err)
			}

			return nil
		})
	}

	_ = g.Wait() //nolint:errcheck // the errors are collected above

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("carrying attestations: %w", err)
	}

	return nil
}

// sourceAttestations are the attestations to carry from one staging image.
type sourceAttestations struct {
	edge         promotion.Edge
	attestations []*provenance.Attestation
}

// carriedAttestations returns the referrer attestations of the digest that
// every enabled policy of the image accepted, if all of them are
// satisfied. Attestations of the children of an index are not about the
// index, so they are never accepted for it, and attestations of the kinds
// the promoter writes itself are not carried.
func carriedAttestations(outcome *provenance.ImageProvenance, digest string) []*provenance.Attestation {
	if outcome == nil || len(outcome.Results) == 0 {
		return nil
	}

	for _, result := range outcome.Results {
		if result == nil || !result.Satisfied {
			return nil
		}
	}

	var carried []*provenance.Attestation

	for _, att := range outcome.Results[0].Accepted {
		if att.Source != provenance.SourceReferrer || att.Digest != digest || att.Layer == "" ||
			slices.Contains(promoterPredicateTypes, att.PredicateType) {
			continue
		}

		if slices.ContainsFunc(carried, func(c *provenance.Attestation) bool { return sameReferrer(c, att) }) {
			continue
		}

		acceptedByAll := true

		for _, result := range outcome.Results[1:] {
			if !slices.ContainsFunc(result.Accepted, func(a *provenance.Attestation) bool { return sameReferrer(a, att) }) {
				acceptedByAll = false

				break
			}
		}

		if acceptedByAll {
			carried = append(carried, att)
		}
	}

	return carried
}

// sameReferrer reports whether two attestations are the same referrer
// layer.
func sameReferrer(a, b *provenance.Attestation) bool {
	return a.Location == b.Location && a.Layer == b.Layer
}

// carryDigestAttestations copies the attestation referrers of one digest
// from its staging images to the canonical registry, skipping those that
// are referrers there already. A failure does not stop the others.
func (di *DefaultPromoterImplementation) carryDigestAttestations(
	ctx context.Context, carried []sourceAttestations,
) error {
	canonical, err := name.NewDigest(canonicalDigestRef(&carried[0].edge))
	if err != nil {
		return fmt.Errorf("parsing canonical reference of %s: %w", carried[0].edge.DstReference(), err)
	}

	existing, err := di.referrerDigests(ctx, canonical)
	if err != nil {
		return fmt.Errorf("listing referrers of %s: %w", canonical, err)
	}

	var errs []error

	for i := range carried {
		for _, att := range carried[i].attestations {
			if existing[att.Location] {
				logrus.Debugf("Attestation %s already carried to %s", att.Location, canonical)

				continue
			}

			if err := di.carryAttestation(ctx, &carried[i].edge, canonical, att); err != nil {
				errs = append(errs, err)

				continue
			}

			existing[att.Location] = true
		}
	}

	return errors.Join(errs...)
}

// carryAttestation copies one attestation referrer. Only a referrer that
// consists of the accepted sigstore bundle and nothing else is copied, so
// that no content that was not verified reaches production.
func (di *DefaultPromoterImplementation) carryAttestation(
	ctx context.Context, edge *promotion.Edge, canonical name.Digest, att *provenance.Attestation,
) error {
	src, err := name.NewDigest(fmt.Sprintf("%s/%s@%s", edge.SrcRegistry.Name, edge.SrcImageTag.Name, att.Location))
	if err != nil {
		return fmt.Errorf("parsing attestation reference: %w", err)
	}

	desc, err := remote.Get(src, append(di.remoteOptions(), remote.WithContext(ctx))...)
	if err != nil {
		return fmt.Errorf("fetching attestation %s: %w", src, err)
	}

	if err := checkBundleReferrer(desc, string(edge.Digest), att); err != nil {
		logrus.Warnf("Not carrying attestation %s: %v", src, err)

		return nil
	}

	dst := canonical.Context().Digest(att.Location)

	logrus.Infof("Carrying %s attestation %s to %s", att.PredicateType, src, dst)

	if err := ratelimit.WithRetry(func() error {
		return craneCopyWithTimeout(ctx, src.String(), dst.String(), di.craneOptions())
	}); err != nil {
		return fmt.Errorf("copying attestation %s to %s: %w", src, dst, err)
	}

	return nil
}

// checkBundleReferrer makes sure that a referrer is a sigstore bundle
// artifact as cosign writes it: an image manifest with the bundle artifact
// type, the empty config, exactly the accepted bundle layer, the image
// digest as subject and, if it has one, the predicate type of the accepted
// attestation as annotation.
func checkBundleReferrer(desc *remote.Descriptor, digest string, att *provenance.Attestation) error {
	if desc.MediaType != types.OCIManifestSchema1 {
		return fmt.Errorf("media type %s is not an OCI image manifest", desc.MediaType)
	}

	mf, err := v1.ParseManifest(bytes.NewReader(desc.Manifest))
	if err != nil {
		return fmt.Errorf("parsing manifest: %w", err)
	}

	predicateType, annotated := mf.Annotations[bundlePredicateTypeAnnotation]

	switch {
	case mf.ArtifactType != provenance.BundleArtifactType:
		return fmt.Errorf("artifact type %q is not a sigstore bundle", mf.ArtifactType)
	case annotated && predicateType != att.PredicateType:
		return fmt.Errorf("annotated predicate type %q is not %s", predicateType, att.PredicateType)
	case mf.Subject == nil || mf.Subject.Digest.String() != digest:
		return fmt.Errorf("the subject is not %s", digest)
	case mf.Config.Digest.String() != emptyConfigDigest:
		return errors.New("the config is not empty")
	case len(mf.Layers) != 1:
		return fmt.Errorf("it has %d layers instead of the bundle", len(mf.Layers))
	case mf.Layers[0].Digest.String() != att.Layer:
		return fmt.Errorf("its layer %s is not the accepted bundle %s", mf.Layers[0].Digest, att.Layer)
	case string(mf.Layers[0].MediaType) != provenance.BundleArtifactType:
		return fmt.Errorf("its layer is a %s, not a sigstore bundle", mf.Layers[0].MediaType)
	}

	return nil
}

// referrerDigests returns the digests of the referrers of a manifest.
func (di *DefaultPromoterImplementation) referrerDigests(ctx context.Context, digest name.Digest) (map[string]bool, error) {
	idx, err := remote.Referrers(digest, append(di.remoteOptions(), remote.WithContext(ctx))...)
	if err != nil {
		return nil, fmt.Errorf("listing referrers: %w", err)
	}

	im, err := idx.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("reading referrers: %w", err)
	}

	digests := make(map[string]bool, len(im.Manifests))
	for i := range im.Manifests {
		digests[im.Manifests[i].Digest.String()] = true
	}

	return digests, nil
}
