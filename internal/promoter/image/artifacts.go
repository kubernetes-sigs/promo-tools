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
	cr "github.com/google/go-containerregistry/pkg/v1/types"

	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// This file has the promoter implementation functions that inspect the
// shape of the promoted OCI objects: images, artifacts and indexes of
// them.

// validateManifestConcurrency limits the number of source manifests
// fetched in parallel while planning.
const validateManifestConcurrency = 20

// errUnsupportedMediaType is returned for manifests that can't be promoted.
var errUnsupportedMediaType = errors.New(
	"unsupported media type, only image manifests and image indexes can be promoted",
)

// validateSourceManifests rejects edges whose source object can't be
// promoted and signed.
//
// Recursive signing only handles image manifests and indexes, including
// artifacts using the image manifest like Helm charts. go-containerregistry
// copies index children of other media types, like the deprecated OCI
// artifact manifest, as blobs, so such an edge would only fail after the
// copy, when signing walks the index and leaves it partially signed.
//
// Image manifests are accepted based on the inventory media types without
// fetching them. Everything else is fetched, and indexes are checked
// recursively.
func (di *DefaultPromoterImplementation) validateSourceManifests(
	ctx context.Context, edges map[promotion.Edge]any, mediaTypes map[image.Digest]cr.MediaType,
) error {
	// Edges repeat the same source digest once per destination and tag.
	seen := make(map[string]struct{}, len(edges))

	for edge := range edges {
		ref := edge.SrcReference()
		if ref == "" || mediaTypes[edge.Digest].IsImage() {
			continue
		}

		seen[ref] = struct{}{}
	}

	// Report every rejected image, in a stable order.
	refs := slices.Sorted(maps.Keys(seen))
	errs := make([]error, len(refs))

	var wg sync.WaitGroup

	sem := make(chan struct{}, validateManifestConcurrency)

	for i, ref := range refs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			errs[i] = di.validateManifest(ctx, ref)
		})
	}

	wg.Wait()

	return errors.Join(errs...)
}

// validateManifest checks that ref is an image manifest or an index whose
// children are image manifests or indexes, recursively.
func (di *DefaultPromoterImplementation) validateManifest(ctx context.Context, ref string) error {
	top, err := name.NewDigest(ref)
	if err != nil {
		return fmt.Errorf("parsing reference %s: %w", ref, err)
	}

	desc, err := di.getManifest(ctx, top)
	if err != nil {
		return err
	}

	switch {
	case desc.MediaType.IsImage():
		return nil
	case desc.MediaType.IsIndex():
		return di.validateIndexChildren(ctx, top, desc)
	default:
		return fmt.Errorf("image %s: media type %q: %w", top, desc.MediaType, errUnsupportedMediaType)
	}
}

// validateIndexChildren checks the children of the index desc, which top
// refers to directly or through parent indexes.
func (di *DefaultPromoterImplementation) validateIndexChildren(
	ctx context.Context, top name.Digest, desc *remote.Descriptor,
) error {
	idx, err := v1.ParseIndexManifest(bytes.NewReader(desc.Manifest))
	if err != nil {
		return fmt.Errorf("parsing index %s of image %s: %w", desc.Digest, top, err)
	}

	// Collect all unsupported children, so that a single run reports them.
	var errs []error

	for _, child := range idx.Manifests {
		switch {
		case child.MediaType.IsImage():
			continue
		case child.MediaType.IsIndex():
			childDesc, err := di.getManifest(ctx, top.Context().Digest(child.Digest.String()))
			if err != nil {
				return err
			}

			if err := di.validateIndexChildren(ctx, top, childDesc); err != nil {
				errs = append(errs, err)
			}
		default:
			errs = append(errs, fmt.Errorf("image %s: index child %s has media type %q: %w",
				top, child.Digest, child.MediaType, errUnsupportedMediaType))
		}
	}

	return errors.Join(errs...)
}

// isContainerImage reports whether ref contains at least one container
// image, which vulnerability scanners can analyze. An image manifest is a
// container image if it has an image config and no artifactType, an index
// if any of its children is one. Everything else is an artifact.
func (di *DefaultPromoterImplementation) isContainerImage(ctx context.Context, ref name.Digest) (bool, error) {
	desc, err := di.getManifest(ctx, ref)
	if err != nil {
		return false, err
	}

	switch {
	case desc.MediaType.IsImage():
		manifest, err := v1.ParseManifest(bytes.NewReader(desc.Manifest))
		if err != nil {
			return false, fmt.Errorf("parsing manifest %s: %w", ref, err)
		}

		return manifest.ArtifactType == "" && manifest.Config.MediaType.IsConfig(), nil

	case desc.MediaType.IsIndex():
		idx, err := v1.ParseIndexManifest(bytes.NewReader(desc.Manifest))
		if err != nil {
			return false, fmt.Errorf("parsing index %s: %w", ref, err)
		}

		for _, child := range idx.Manifests {
			if !child.MediaType.IsImage() && !child.MediaType.IsIndex() {
				continue
			}

			ok, err := di.isContainerImage(ctx, ref.Context().Digest(child.Digest.String()))
			if err != nil {
				return false, err
			}

			if ok {
				return true, nil
			}
		}

		return false, nil

	default:
		return false, nil
	}
}

// getManifest fetches the manifest of ref.
func (di *DefaultPromoterImplementation) getManifest(ctx context.Context, ref name.Digest) (*remote.Descriptor, error) {
	var desc *remote.Descriptor

	if err := ratelimit.WithRetry(func() error {
		var getErr error

		desc, getErr = remote.Get(ref, append(di.remoteOptions(), remote.WithContext(ctx))...)
		if getErr != nil {
			return fmt.Errorf("getting: %w", getErr)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("getting manifest %s: %w", ref, err)
	}

	return desc, nil
}
