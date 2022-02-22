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
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	reg "sigs.k8s.io/promo-tools/v3/legacy/dockerregistry"
	options "sigs.k8s.io/promo-tools/v3/promoter/image/options"
)

// Run a snapshot
func (di *DefaultPromoterImplementation) Snapshot(opts *options.Options, rii reg.RegInvImage) error {
	// Run the snapshot
	var snapshot string
	switch strings.ToLower(opts.OutputFormat) {
	case "csv":
		snapshot = rii.ToCSV()
	case "yaml":
		snapshot = rii.ToYAML(reg.YamlMarshalingOpts{})
	default:
		// In the previous cli/run it took any malformed format string. Now we err.
		return errors.Errorf("invalid snapshot output format: %s", opts.OutputFormat)
	}

	// TODO: Maybe store the snapshot somewhere?
	di.PrintSection("END (SNAPSHOT)", opts.Confirm)
	fmt.Println(snapshot)
	return nil
}

func (di *DefaultPromoterImplementation) GetSnapshotSourceRegistry(
	opts *options.Options,
) (*reg.RegistryContext, error) {
	// Build the source registry:
	srcRegistry := &reg.RegistryContext{
		ServiceAccount: opts.SnapshotSvcAcct,
		Src:            true,
	}

	// The only difference when running from Snapshot or
	// ManifestBasedSnapshotOf will be the Name property
	// of the source registry
	if opts.Snapshot != "" {
		srcRegistry.Name = reg.RegistryName(opts.Snapshot)
	} else if opts.ManifestBasedSnapshotOf == "" {
		srcRegistry.Name = reg.RegistryName(opts.ManifestBasedSnapshotOf)
	} else {
		return nil, errors.New(
			"when snapshotting, Snapshot or ManifestBasedSnapshotOf have to be set",
		)
	}

	return srcRegistry, nil
}

// GetSnapshotManifest creates the manifest list from the
// specified snapshot source
func (di *DefaultPromoterImplementation) GetSnapshotManifests(
	opts *options.Options,
) ([]reg.Manifest, error) {
	// Build the source registry:
	srcRegistry, err := di.GetSnapshotSourceRegistry(opts)
	if err != nil {
		return nil, errors.Wrap(err, "building source registry for snapshot")
	}

	// Add it to a new manifest and return it:
	return []reg.Manifest{
		{
			Registries: []reg.RegistryContext{
				*srcRegistry,
			},
			Images: []reg.Image{},
		},
	}, nil
}

// AppendManifestToSnapshot checks if a manifest was specified in the
// options passed to the promoter. If one is found, we parse it and
// append it to the list of manifests generated for the snapshot
// during GetSnapshotManifests()
func (di *DefaultPromoterImplementation) AppendManifestToSnapshot(
	opts *options.Options, mfests []reg.Manifest,
) ([]reg.Manifest, error) {
	// If no manifest was passed in the options, we return the
	// same list of manifests unchanged
	if opts.Manifest == "" {
		logrus.Info("No manifest defined, not appending to snapshot")
		return mfests, nil
	}

	// Parse the specified manifest and append it to the list
	mfest, err := reg.ParseManifestFromFile(opts.Manifest)
	if err != nil {
		return nil, errors.Wrap(err, "parsing specified manifest")
	}

	return append(mfests, mfest), nil
}

//
func (di *DefaultPromoterImplementation) GetRegistryImageInventory(
	opts *options.Options, mfests []reg.Manifest,
) (reg.RegInvImage, error) {
	// I'm pretty sure the registry context here can be the same for
	// both snapshot sources and when running in the original cli/run,
	// In the 2nd case (Snapshot), it was recreated like we do here.
	sc, err := di.MakeSyncContext(opts, mfests)
	if err != nil {
		return nil, errors.Wrap(err, "making sync context for registry inventory")
	}

	srcRegistry, err := di.GetSnapshotSourceRegistry(opts)
	if err != nil {
		return nil, errors.Wrap(err, "creting source registry for image inventory")
	}

	if len(opts.ManifestBasedSnapshotOf) > 0 {
		promotionEdges, err := reg.ToPromotionEdges(mfests)
		if err != nil {
			return nil, errors.Wrap(
				err, "converting list of manifests to edges for promotion",
			)
		}

		// Create the registry inventory
		rii := reg.EdgesToRegInvImage(
			promotionEdges,
			opts.ManifestBasedSnapshotOf,
		)

		if opts.MinimalSnapshot {
			sc.ReadRegistries(
				[]reg.RegistryContext{*srcRegistry},
				true,
				reg.MkReadRepositoryCmdReal,
			)

			sc.ReadGCRManifestLists(reg.MkReadManifestListCmdReal)
			rii = sc.RemoveChildDigestEntries(rii)
		}

		return rii, nil
	}

	sc.ReadRegistries(
		[]reg.RegistryContext{*srcRegistry},
		// Read all registries recursively, because we want to produce a
		// complete snapshot.
		true,
		reg.MkReadRepositoryCmdReal,
	)

	rii, ok := sc.Inv[mfests[0].Registries[0].Name]
	if !ok {
		logrus.Debugf("Retrieved inventory: %+v", sc.Inv)
		return nil, errors.Errorf(
			"unable to find inventory for registry %s",
			mfests[0].Registries[0].Name,
		)
	}
	if opts.SnapshotTag != "" {
		rii = reg.FilterByTag(rii, opts.SnapshotTag)
	}

	if opts.MinimalSnapshot {
		logrus.Info("removing tagless child digests of manifest lists")
		sc.ReadGCRManifestLists(reg.MkReadManifestListCmdReal)
		rii = sc.RemoveChildDigestEntries(rii)
	}
	return rii, nil
}
