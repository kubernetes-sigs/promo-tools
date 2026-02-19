/*
Copyright 2022 The Kubernetes Authors.

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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	cr "github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/sirupsen/logrus"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// Run a snapshot.
func (di *DefaultPromoterImplementation) Snapshot(opts *options.Options, rii registry.RegInvImage) error {
	// Run the snapshot
	var snapshot string
	switch strings.ToLower(opts.OutputFormat) {
	case "csv":
		snapshot = rii.ToCSV()
	case "yaml":
		snapshot = rii.ToYAML(registry.YamlMarshalingOpts{})
	default:
		// In the previous cli/run it took any malformed format string. Now we err.
		return fmt.Errorf("invalid snapshot output format: %s", opts.OutputFormat)
	}

	// TODO: Maybe store the snapshot somewhere?
	di.PrintSection("END (SNAPSHOT)", opts.Confirm)
	fmt.Println(snapshot)
	return nil
}

func (di *DefaultPromoterImplementation) GetSnapshotSourceRegistry(
	opts *options.Options,
) (*registry.Context, error) {
	// Build the source registry:
	srcRegistry := &registry.Context{
		ServiceAccount: opts.SnapshotSvcAcct,
		Src:            true,
	}

	// The only difference when running from Snapshot or
	// ManifestBasedSnapshotOf will be the Name property
	// of the source registry
	switch {
	case opts.Snapshot != "":
		srcRegistry.Name = image.Registry(opts.Snapshot)
	case opts.ManifestBasedSnapshotOf != "":
		srcRegistry.Name = image.Registry(opts.ManifestBasedSnapshotOf)
	default:
		return nil, errors.New(
			"when snapshotting, Snapshot or ManifestBasedSnapshotOf have to be set",
		)
	}

	return srcRegistry, nil
}

// GetSnapshotManifest creates the manifest list from the
// specified snapshot source.
func (di *DefaultPromoterImplementation) GetSnapshotManifests(
	opts *options.Options,
) ([]schema.Manifest, error) {
	// Build the source registry:
	srcRegistry, err := di.GetSnapshotSourceRegistry(opts)
	if err != nil {
		return nil, errors.New("building source registry for snapshot")
	}

	// Add it to a new manifest and return it:
	return []schema.Manifest{
		{
			Registries: []registry.Context{
				*srcRegistry,
			},
			Images: []registry.Image{},
		},
	}, nil
}

// AppendManifestToSnapshot checks if a manifest was specified in the
// options passed to the promoter. If one is found, we parse it and
// append it to the list of manifests generated for the snapshot
// during GetSnapshotManifests().
func (di *DefaultPromoterImplementation) AppendManifestToSnapshot(
	opts *options.Options, mfests []schema.Manifest,
) ([]schema.Manifest, error) {
	// If no manifest was passed in the options, we return the
	// same list of manifests unchanged
	if opts.Manifest == "" {
		logrus.Info("No manifest defined, not appending to snapshot")
		return mfests, nil
	}

	// Parse the specified manifest and append it to the list
	mfest, err := schema.ParseManifestFromFile(opts.Manifest)
	if err != nil {
		return nil, fmt.Errorf("parsing specified manifest: %w", err)
	}

	return append(mfests, mfest), nil
}

func (di *DefaultPromoterImplementation) GetRegistryImageInventory(
	opts *options.Options, mfests []schema.Manifest,
) (registry.RegInvImage, error) {
	srcRegistry, err := di.GetSnapshotSourceRegistry(opts)
	if err != nil {
		return nil, fmt.Errorf("creating source registry for image inventory: %w", err)
	}

	registryConfig := registry.RegistryConfigFromContext(*srcRegistry)

	if opts.ManifestBasedSnapshotOf != "" {
		edges, err := promotion.ToEdges(mfests)
		if err != nil {
			return nil, fmt.Errorf("converting list of manifests to edges for promotion: %w", err)
		}

		// Create the registry inventory from manifest edges
		rii := promotion.EdgesToRegInvImage(
			edges,
			opts.ManifestBasedSnapshotOf,
		)

		if opts.MinimalSnapshot {
			inv, err := di.registryProvider.ReadRegistries(
				context.Background(),
				[]registry.RegistryConfig{registryConfig},
				true,
			)
			if err != nil {
				return nil, fmt.Errorf("reading registry for minimal snapshot: %w", err)
			}

			rii = removeChildDigests(inv, rii, srcRegistry.Name, di.craneOptions()...)
		}

		return rii, nil
	}

	// Direct snapshot path: read the registry
	inv, err := di.registryProvider.ReadRegistries(
		context.Background(),
		[]registry.RegistryConfig{registryConfig},
		true,
	)
	if err != nil {
		return nil, fmt.Errorf("reading registries: %w", err)
	}

	rii := inv.Images[mfests[0].Registries[0].Name]
	if opts.SnapshotTag != "" {
		rii = promotion.FilterByTag(rii, opts.SnapshotTag)
	}

	if opts.MinimalSnapshot {
		logrus.Info("removing tagless child digests of manifest lists")
		rii = removeChildDigests(inv, rii, mfests[0].Registries[0].Name, di.craneOptions()...)
	}

	return rii, nil
}

// removeChildDigests filters out tagless entries from rii that are children
// of manifest lists. It uses the media types from the inventory to identify
// manifest list digests, fetches their manifests to find child digests,
// and removes tagless children.
func removeChildDigests(
	inv *registry.Inventory,
	rii registry.RegInvImage,
	registryName image.Registry,
	opts ...crane.Option,
) registry.RegInvImage {
	// Build a set of child digests by reading manifest lists from the registry.
	childDigests := make(map[image.Digest]bool)
	regInv := inv.Images[registryName]

	for imageName, digestTags := range regInv {
		for digest := range digestTags {
			mediaType := inv.MediaTypes[digest]
			if mediaType != cr.DockerManifestList && mediaType != cr.OCIImageIndex {
				continue
			}

			// Fetch the manifest list to get child digests
			ref := fmt.Sprintf("%s/%s@%s", registryName, imageName, digest)
			rawManifest, err := crane.Manifest(ref, opts...)
			if err != nil {
				logrus.Warnf("failed to read manifest list %s: %v", ref, err)
				continue
			}

			var idx v1.IndexManifest
			if err := json.Unmarshal(rawManifest, &idx); err != nil {
				logrus.Warnf("failed to parse manifest list %s: %v", ref, err)
				continue
			}

			for i := range idx.Manifests {
				childDigests[image.Digest(idx.Manifests[i].Digest.String())] = true
			}
		}
	}

	// Filter out tagless children
	filtered := make(registry.RegInvImage)
	for imageName, digestTags := range rii {
		for digest, tagSlice := range digestTags {
			// If this digest is a child of a manifest list and has no tags,
			// filter it out.
			if childDigests[digest] && len(tagSlice) == 0 {
				continue
			}

			if filtered[imageName] == nil {
				filtered[imageName] = make(registry.DigestTags)
			}
			filtered[imageName][digest] = tagSlice
		}
	}

	return filtered
}
