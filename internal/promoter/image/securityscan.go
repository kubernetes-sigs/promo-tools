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
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/sirupsen/logrus"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/vuln"
)

// ScanEdges runs vulnerability scans on the new images detected by the
// promoter using the configured vuln.Scanner. Artifacts that contain no
// container image are reported as not applicable and not scanned.
func (di *DefaultPromoterImplementation) ScanEdges(
	ctx context.Context,
	opts *options.Options,
	promotionEdges map[promotion.Edge]any,
) error {
	if opts.SeverityThreshold <= 0 {
		logrus.Info("Vulnerability scanning disabled (threshold <= 0)")

		return nil
	}

	threshold := vuln.Severity(opts.SeverityThreshold)

	// Edges repeat the same source digest once per destination and tag.
	applicable := make(map[string]bool, len(promotionEdges))

	for edge := range promotionEdges {
		ref := edge.SrcReference()
		if ref == "" {
			continue
		}

		isImage, ok := applicable[ref]
		if !ok {
			digest, err := name.NewDigest(ref)
			if err != nil {
				return fmt.Errorf("parsing reference %s: %w", ref, err)
			}

			isImage, err = di.isContainerImage(ctx, digest)
			if err != nil {
				return fmt.Errorf("inspecting %s: %w", ref, err)
			}

			applicable[ref] = isImage

			if !isImage {
				logrus.Infof("Vulnerability scan not applicable to %s: not a container image", ref)
			}
		}

		// Artifacts like Helm charts have no image content, and scanners
		// report them as clean, so don't scan them at all.
		if !isImage {
			continue
		}

		result, err := di.vulnScanner.Scan(ctx, ref)
		if err != nil {
			return fmt.Errorf("scanning %s: %w", ref, err)
		}

		if result.ExceedsSeverity(threshold) {
			return fmt.Errorf(
				"image %s has vulnerabilities at or above threshold %d (highest: %d)",
				ref, threshold, result.HighestSeverity,
			)
		}
	}

	return nil
}
