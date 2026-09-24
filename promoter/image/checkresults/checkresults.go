/*
Copyright 2023 The Kubernetes Authors.

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

package checkresults

import (
	"fmt"
	"strings"
	"time"

	"sigs.k8s.io/promo-tools/v4/image/consts"
)

// Image is a digest promoted to the production registry.
type Image struct {
	// Name is the image name below the production repository, for example
	// "kube-apiserver" or "sig-storage/csi-provisioner".
	Name string

	// Digest is the digest of the image, for example "sha256:abc…".
	Digest string

	// Tags are the tags of the digest. They are empty for digests promoted
	// without a tag, which are attested but not signed by the promoter.
	Tags []string

	// Uploaded is the time the digest was uploaded to the registry.
	Uploaded time.Time

	// Attest is true when the image must have a promotion attestation,
	// because it was promoted after promotion started to write them.
	Attest bool
}

// String returns the production reference of the image, with its tags.
func (i *Image) String() string {
	ref := consts.ProdRegistry + "/" + i.Name + "@" + i.Digest
	if len(i.Tags) == 0 {
		return ref
	}

	return fmt.Sprintf("%s (%s)", ref, strings.Join(i.Tags, ", "))
}

// Status is the signature and attestation status of an image.
type Status struct {
	Image

	// Signed is true when the image has a signature of the expected
	// identity. It is only checked for images with tags.
	Signed bool

	// Attested is true when the image has a promotion attestation of the
	// expected identity. It is only checked when Attest is set.
	Attested bool
}

// Unsigned reports whether the image needs a signature but has none.
func (s *Status) Unsigned() bool {
	return len(s.Tags) > 0 && !s.Signed
}

// Unattested reports whether the image needs a promotion attestation but
// has none.
func (s *Status) Unattested() bool {
	return s.Attest && !s.Attested
}

// Results are the statuses of the checked images.
type Results []Status

// Unsigned returns the images that need a signature but have none.
func (r Results) Unsigned() Results {
	return r.filter((*Status).Unsigned)
}

// Unattested returns the images that need a promotion attestation but
// have none.
func (r Results) Unattested() Results {
	return r.filter((*Status).Unattested)
}

// Problems returns the images that are unsigned or unattested.
func (r Results) Problems() Results {
	return r.filter(func(s *Status) bool {
		return s.Unsigned() || s.Unattested()
	})
}

// Images returns the checked images.
func (r Results) Images() []Image {
	images := make([]Image, 0, len(r))
	for i := range r {
		images = append(images, r[i].Image)
	}

	return images
}

func (r Results) filter(keep func(*Status) bool) Results {
	var res Results

	for i := range r {
		if keep(&r[i]) {
			res = append(res, r[i])
		}
	}

	return res
}
