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
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
)

// This file has all the promoter implementation functions
// related to image promotion.

// ParseManifests reads the manifest file or manifest directory
// and parses them to return a slice of Manifest objects.
func (di *DefaultPromoterImplementation) ParseManifests(opts *options.Options) ([]schema.Manifest, error) {
	// If the options have a manifest file defined, we use that one
	if opts.Manifest != "" {
		mfest, err := schema.ParseManifestFromFile(opts.Manifest)
		if err != nil {
			return nil, fmt.Errorf("parsing the manifest file: %w", err)
		}

		return []schema.Manifest{mfest}, nil
	}

	// The thin manifests
	if opts.ThinManifestDir != "" {
		mfests, err := schema.ParseThinManifestsFromDir(opts.ThinManifestDir, opts.UseProwManifestDiff)
		if err != nil {
			return nil, fmt.Errorf("parsing thin manifest directory: %w", err)
		}

		return mfests, nil
	}

	return nil, nil
}

// GetPromotionEdges checks the manifests and determines from
// them the promotion edges, ie the images that need to be
// promoted.
func (di *DefaultPromoterImplementation) GetPromotionEdges(
	ctx context.Context, opts *options.Options, mfests []schema.Manifest,
) (map[promotion.Edge]any, error) {
	// Convert manifests to edges
	edges, err := promotion.ToEdges(mfests)
	if err != nil {
		return nil, fmt.Errorf("converting manifests to edges: %w", err)
	}

	// Collect registries we need to read (full paths including image names)
	regs := promotion.GetRegistriesToRead(edges)
	configs := registry.RegistryConfigsFromContexts(regs)

	// Collect base registries (without image name suffixes) for correct
	// inventory keying in splitByKnownRegistries.
	baseRegs := promotion.GetBaseRegistries(edges)
	baseConfigs := registry.RegistryConfigsFromContexts(baseRegs)

	for _, cfg := range configs {
		logrus.Debugf("reading registry %s (src=%v)", cfg.Name, cfg.Src)
	}

	// Read registry inventories (non-recursive, specific repos only)
	inv, err := di.registryProvider.ReadRegistries(ctx, configs, false, baseConfigs)
	if err != nil {
		return nil, fmt.Errorf("reading registries: %w", err)
	}

	// Filter to only edges that need promotion
	filtered, clean := promotion.GetPromotionCandidates(edges, inv.Images)
	if !clean {
		return nil, errors.New("encountered errors during edge filtering")
	}

	return filtered, nil
}

// EdgesFromManifests converts manifests directly to promotion edges without
// filtering against live registry state. This is used by the standalone
// replicate-signatures subcommand which needs ALL edges (not just unsynced ones)
// to ensure signatures exist everywhere.
func (di *DefaultPromoterImplementation) EdgesFromManifests(
	mfests []schema.Manifest,
) (map[promotion.Edge]any, error) {
	edges, err := promotion.ToEdges(mfests)
	if err != nil {
		return nil, fmt.Errorf("converting manifests to edges: %w", err)
	}

	return edges, nil
}

// PromoteImages copies images for a set of promotion edges.
func (di *DefaultPromoterImplementation) PromoteImages(
	ctx context.Context,
	opts *options.Options,
	edges map[promotion.Edge]any,
) error {
	if len(edges) == 0 {
		logrus.Info("Nothing to promote.")

		return nil
	}

	logrus.Info("Pending promotions:")

	for edge := range edges {
		logrus.Infof(
			"%s/%s:%s (%s) to %s/%s",
			edge.SrcRegistry.Name,
			edge.SrcImageTag.Name,
			edge.SrcImageTag.Tag,
			edge.Digest,
			edge.DstRegistry.Name,
			edge.DstImageTag.Name,
		)
	}

	total := len(edges)
	logrus.Infof("Promoting %d images using %d threads", total, opts.Threads)

	var completed atomic.Int64

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.Threads)

	for edge := range edges {
		g.Go(func() error {
			srcVertex := promotion.ToFQIN(
				edge.SrcRegistry.Name, edge.SrcImageTag.Name, edge.Digest,
			)

			var dstVertex string
			if edge.DstImageTag.Tag != "" {
				dstVertex = promotion.ToPQIN(
					edge.DstRegistry.Name, edge.DstImageTag.Name, edge.DstImageTag.Tag,
				)
			} else {
				dstVertex = promotion.ToFQIN(
					edge.DstRegistry.Name, edge.DstImageTag.Name, edge.Digest,
				)
			}

			logrus.Infof("Copying %s to %s", srcVertex, dstVertex)

			start := time.Now()

			if err := ratelimit.WithRetry(func() error {
				return di.registryProvider.CopyImage(ctx, srcVertex, dstVertex)
			}); err != nil {
				return fmt.Errorf("copying %s to %s: %w", srcVertex, dstVertex, err)
			}

			logrus.Infof("Copied %s (%d/%d) in %s", dstVertex, completed.Add(1), total, time.Since(start).Round(time.Millisecond))

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("running image promotion: %w", err)
	}

	return nil
}
