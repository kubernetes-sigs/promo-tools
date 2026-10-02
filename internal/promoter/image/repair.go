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
	"net/http"
	"slices"
	"sync"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
)

// FindAttestationRepairs returns the edges of the promoted digests that
// miss what the attest phase writes besides the promotion records: a
// staging attestation that is not carried, or, with verification
// summaries enabled, the promoter's summary of the digest or of an
// unlisted platform manifest of an index. Promoted digests are no
// promotion candidates, so nothing else retries a failed attest phase for
// them. Only the referrers are compared: whether the policies accept a
// missing attestation is decided when the images are evaluated again, so
// a staging attestation they never accept brings its image back on every
// run. Digests that are not in the canonical registry are left out, and a
// failed check does not keep the others from being repaired.
func (di *DefaultPromoterImplementation) FindAttestationRepairs(
	ctx context.Context, opts *options.Options, edges map[promotion.Edge]any,
) (map[promotion.Edge]any, error) {
	if !opts.SignImages || len(edges) == 0 {
		return map[promotion.Edge]any{}, nil
	}

	groups := groupEdgesByIdentityDigest(edges, withTagless)

	// Platform manifests that are listed digests of their own are checked
	// as such, not as children of their index.
	listed := make(map[string]bool, len(groups))
	for _, group := range groups {
		listed[targetIdentity(&group[0])+"@"+string(group[0].Digest)] = true
	}

	var (
		g       errgroup.Group
		mu      sync.Mutex
		errs    []error
		repairs = map[promotion.Edge]any{}
	)

	g.SetLimit(opts.MaxSignatureOps)

	for _, group := range groups {
		g.Go(func() error {
			missing, err := di.missingAttestation(ctx, opts, group, listed)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				errs = append(errs, err)

				return nil
			}

			if missing == "" {
				return nil
			}

			logrus.Infof("Checking the attestations of %s@%s again: %s", targetIdentity(&group[0]), group[0].Digest, missing)

			for _, edge := range group {
				repairs[edge] = nil
			}

			return nil
		})
	}

	_ = g.Wait() //nolint:errcheck // the errors are collected above

	if err := errors.Join(errs...); err != nil {
		return repairs, fmt.Errorf("checking the attestations of promoted images: %w", err)
	}

	return repairs, nil
}

// missingAttestation returns what the promoted digest of the edges misses,
// or an empty string if nothing.
func (di *DefaultPromoterImplementation) missingAttestation(
	ctx context.Context, opts *options.Options, group []promotion.Edge, listed map[string]bool,
) (string, error) {
	edge := group[0]

	canonical, err := name.NewDigest(canonicalDigestRef(&edge))
	if err != nil {
		return "", fmt.Errorf("parsing canonical reference of %s: %w", edge.DstReference(), err)
	}

	// The manifest also tells whether it is an index whose platform
	// manifests need summaries.
	desc, err := remote.Get(canonical, append(di.remoteOptions(), remote.WithContext(ctx))...)
	if err != nil {
		if terr, ok := errors.AsType[*transport.Error](err); ok && terr.StatusCode == http.StatusNotFound {
			logrus.Debugf("Not repairing %s: not promoted", canonical)

			return "", nil
		}

		return "", fmt.Errorf("fetching %s: %w", canonical, err)
	}

	carried, err := di.referrerDigests(ctx, canonical)
	if err != nil {
		return "", fmt.Errorf("listing referrers of %s: %w", canonical, err)
	}

	checked := map[string]bool{}

	for i := range group {
		src := &group[i]
		if checked[src.SrcReference()] {
			continue
		}

		checked[src.SrcReference()] = true

		staged, err := di.stagingAttestationReferrers(ctx, src)
		if err != nil {
			return "", err
		}

		for _, digest := range staged {
			if !carried[digest] {
				return "staging attestation " + digest + " is not in the canonical registry", nil
			}
		}
	}

	if !opts.VerificationSummaries {
		return "", nil
	}

	identity, err := summaryIdentity(opts)
	if err != nil {
		return "", err
	}

	exists, err := di.hasVerificationSummary(canonical, identity)
	if err != nil {
		return "", fmt.Errorf("checking the verification summaries of %s: %w", canonical, err)
	}

	if !exists {
		return "no verification summary", nil
	}

	children, err := unlistedPlatformManifests(desc, &edge, listed)
	if err != nil {
		return "", fmt.Errorf("listing the children of %s: %w", canonical, err)
	}

	for _, child := range children {
		exists, err := di.hasVerificationSummary(canonical.Context().Digest(child), identity)
		if err != nil {
			return "", fmt.Errorf("checking the verification summaries of %s@%s: %w", canonical.Context(), child, err)
		}

		if !exists {
			return "no verification summary for platform manifest " + child, nil
		}
	}

	return "", nil
}

// stagingAttestationReferrers returns the digests of the attestation
// referrers of the staging image of an edge that carrying could copy:
// sigstore bundles annotated with a predicate type the promoter does not
// write itself. Bundles without the annotation are not compared. The
// annotation identifies the bundles: registries don't all list the
// artifact type of a referrer, some list the media type of its config.
func (di *DefaultPromoterImplementation) stagingAttestationReferrers(
	ctx context.Context, edge *promotion.Edge,
) ([]string, error) {
	src, err := name.NewDigest(edge.SrcReference())
	if err != nil {
		return nil, fmt.Errorf("parsing staging reference %s: %w", edge.SrcReference(), err)
	}

	idx, err := remote.Referrers(src, append(di.remoteOptions(), remote.WithContext(ctx))...)
	if err != nil {
		return nil, fmt.Errorf("listing referrers of %s: %w", src, err)
	}

	im, err := idx.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("reading referrers of %s: %w", src, err)
	}

	var digests []string

	for i := range im.Manifests {
		desc := &im.Manifests[i]

		predicateType, ok := desc.Annotations[bundlePredicateTypeAnnotation]
		if !ok || slices.Contains(promoterPredicateTypes, predicateType) {
			continue
		}

		digests = append(digests, desc.Digest.String())
	}

	return digests, nil
}
