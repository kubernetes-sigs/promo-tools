/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package inventory

import (
	"context"
	"k8s.io/klog"
)

// GrowManifestOptions holds the  parameters for modifying manifests.
type GrowManifestOptions struct {
	// BaseDir is the directory containing the thin promoter manifests.
	BaseDir string
	// StagingSubproject is the staging subproject name to filter by.
	SubprojectDir string
	// ImageName is the image name to filter by.
	ImageName ImageName
	// StagingDigest is the image digest in staging to use.
	StagingDigest Digest
	// StagingTag is the image tag in staging to use.
	StagingTag Tag
}

// PopulateDefaults sets the default values for GrowManifestOptions.
func (o *GrowManifestOptions) PopulateDefaults() {
	// There are no fields with non-empty default values
	// (but we still want to follow the PopulateDefaults pattern)
}

// GrowManifest modifies a manifest by adding images into it.
func GrowManifest(
	ctx context.Context,
	o GrowManifestOptions,
) (Manifest, error) {

	var err error
	var manifests []Manifest
	m := Manifest{}
	// (1) Using the SubprojectDir, scan the correct manifest and get the source
	// registry. This is the registry to read with filters.
	//
	// INPUT: staging GCR + filters
	// OUTPUT: RegInvImage of images to promote

	// (2) Scan the BaseDir to identify the manifest folders to modify.
	//
	// INPUT: On-disk manifest
	// OUTPU: Manifest data structure (with RegInvImage)

	// (3) Inject (1)'s output into (2)'s output to create a larger RegInvImage.
	//
	// INPUT: RegInvImage (to promote) + RegInvImage (found on-disk)
	// OUTPUT: RegInvImage (sum total)

	manifests, err = ParseThinManifestsFromDir(o.BaseDir)
	if err != nil {
		klog.Exitln(err)
	}

	klog.Infof("%d manifests parsed", len(manifests))

	// (4) Write back RegInvImage as Manifest ([]Image field}) back onto disk.
	//
	// INPUT: RegInvImage (sum total) (or maybe as a Manifest?)
	// OUTPUT: Overwrite existing file with YAML.

	return m, nil
}
