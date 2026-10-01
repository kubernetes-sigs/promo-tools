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
	"slices"
	"sync"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
)

// discoveryConcurrency is the number of staging images whose attestations
// are discovered in parallel.
const discoveryConcurrency = 10

// SetDiscoverer sets the attestation discoverer used during promotion.
// Discovery is skipped when it is nil.
func (p *Promoter) SetDiscoverer(d provenance.Discoverer) {
	p.discoverer = d
}

// Discoveries returns the attestations discovered for the staging images of
// the last promotion run, by source digest reference, including the
// promoted images the repair phase evaluated again. Images whose discovery
// failed are left out. It is nil if that run stopped before the provenance
// phase, or without a discoverer.
func (p *Promoter) Discoveries() map[string]*provenance.Discovery {
	return p.discoveries
}

// Provenance returns what the provenance phase of the last promotion run
// concluded for each staging image, by source digest reference: the
// provenance policies that apply and their results. It also holds the
// results of the promoted images the repair phase evaluated again, which
// blocked nothing. It is nil if that run stopped before the provenance
// phase.
func (p *Promoter) Provenance() map[string]*provenance.ImageProvenance {
	return p.provenance
}

// discoverStagingImages discovers and logs the attestations of the source
// images of the edges, by source reference. It returns nil without a
// discoverer. Discovery only reports: a failure is logged as a warning,
// which tells it apart from an image without attestations, and the image is
// left out.
func (p *Promoter) discoverStagingImages(
	ctx context.Context, edges map[promotion.Edge]any,
) map[string]*provenance.Discovery {
	if p.discoverer == nil {
		return nil
	}

	// Edges repeat the same source digest once per destination region and
	// tag, so discover each source reference only once.
	refs := map[string]struct{}{}

	for edge := range edges {
		if ref := edge.SrcReference(); ref != "" {
			refs[ref] = struct{}{}
		}
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	discoveries := make(map[string]*provenance.Discovery, len(refs))
	failures := make(map[string]error)
	sem := make(chan struct{}, discoveryConcurrency)

	for ref := range refs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			discovery, err := p.discoverer.Discover(ctx, ref)
			if err == nil && discovery == nil {
				err = errors.New("no discovery returned")
			}

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				failures[ref] = err

				return
			}

			discoveries[ref] = discovery
		})
	}

	wg.Wait()

	// Log in a stable order, so the report of an image stays together.
	sorted := make([]string, 0, len(refs))
	for ref := range refs {
		sorted = append(sorted, ref)
	}

	slices.Sort(sorted)

	for _, ref := range sorted {
		if err, ok := failures[ref]; ok {
			logrus.Warnf("Discovering attestations of %s failed: %v", ref, err)

			continue
		}

		logDiscovery(ref, discoveries[ref])
	}

	return discoveries
}

// logDiscovery logs the attestations found for a staging image.
func logDiscovery(ref string, discovery *provenance.Discovery) {
	lines := discovery.Summary()
	if len(lines) == 0 {
		logrus.Infof("No attestations found for %s", ref)

		return
	}

	logrus.Infof("Attestations found for %s:", ref)

	for _, line := range lines {
		logrus.Infof("  %s", line)
	}
}
