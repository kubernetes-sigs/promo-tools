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

package types

import (
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
)

// ImageMirrorer writes image layers to some backend mirror storage
type ImageMirrorer interface {
	// Mirror ensures the layer blobs for a supplied Image are written to the
	// mirror's backend storage
	Mirror(
		string, // the image URI/reference
		ggcrv1.Image,
	) error
	// ID returns the identifier for the mirror
	ID() string
}

// MirrorManager manages the syncing of image layer blobs across a set of
// mirrors
type MirrorManager interface {
	// SyncImage replicates the layer blobs to all mirrors for the image with
	// the supplied Image URI
	SyncImage(
		string, // the image URI/reference
		ggcrv1.Image,
	) error
}
