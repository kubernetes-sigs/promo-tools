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

package manager

import (
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/sirupsen/logrus"

	awsmirror "sigs.k8s.io/promo-tools/v3/mirror/pkg/aws"
	"sigs.k8s.io/promo-tools/v3/mirror/pkg/config"
	"sigs.k8s.io/promo-tools/v3/mirror/pkg/types"
)

// mirrorManager manages multiple mirrors across different cloud providers and
// cloud provider regions. It implements the types.MirrorManager interface.
type mirrorManager struct {
	cfg     *config.Config
	mirrors []types.ImageMirrorer
}

// imageSync implements error and provides information about the
// results of a Sync operation across multiple mirrors.
type imageSyncResult struct {
	// errors is a map, keyed by mirror ID, of errors that occurred during a
	// Sync operation
	errors map[string]error
}

// Success returns true if all mirrors were able to successfully replicate
// the image's layers, false otherwise
func (r *imageSyncResult) Error() string {
	for _, v := range r.errors {
		if v != nil {
			return v.Error()
		}
	}
	return ""
}

// New returns a new MirrorManager that can replicate image layers to a set of
// mirrors
func New(cfg *config.Config) (types.MirrorManager, error) {
	mirrors := []types.ImageMirrorer{}
	if cfg.AWS != nil {
		for _, region := range cfg.AWS.Regions {
			bucket := awsmirror.BucketName(cfg.AWS.BucketPrefix, region)
			m, err := awsmirror.NewBucketMirror(bucket, region)
			if err != nil {
				return nil, err
			}
			mirrors = append(mirrors, m)
		}
	}
	return &mirrorManager{
		cfg:     cfg,
		mirrors: mirrors,
	}, nil
}

// SyncImage replicates the layer blobs to all mirrors for the image with the
// supplied Image URI
//
// NOTE(jaypipes): Just using a brute force serialized approach for now. We can
// look into parallelizing this if we see a need in the future.
func (mm *mirrorManager) SyncImage(
	imageURI string, // the image URI/reference
	image ggcrv1.Image,
) error {
	r := imageSyncResult{
		errors: make(map[string]error, len(mm.mirrors)),
	}

	for _, mirror := range mm.mirrors {
		mid := mirror.ID()
		logrus.Debugf(
			"[%s] beginning sync of image %s",
			mid, imageURI,
		)
		r.errors[mid] = mirror.Mirror(imageURI, image)
	}
	for _, err := range r.errors {
		if err != nil {
			return &r
		}
	}
	return nil
}
