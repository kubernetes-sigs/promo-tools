/*
Copyright 2020 The Kubernetes Authors.

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

package manifest

import (
	"context"
	"io/ioutil"
	"path"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"golang.org/x/xerrors"

	reg "sigs.k8s.io/promo-tools/v3/legacy/dockerregistry"
)

const (
	// This is a banned tag. It is not allowed to be manipulated with this tool.
	latestTag = "latest"
)

// GrowOptions holds the  parameters for modifying manifests.
type GrowOptions struct {
	// BaseDir is the directory containing the thin promoter manifests.
	BaseDir string
	// StagingRepo is the staging subproject repo to read from. If no filters
	// are provided, all images are attempted to be promoted as-is without any
	// modifications.
	StagingRepo reg.RegistryName
	// FilterImage is the image (name) to filter by. Optional.
	FilterImage reg.ImageName
	// FilterDigest is the image digest to filter by. Optional.
	FilterDigest reg.Digest
	// FilterTag is the image tag to filter by. Optional.
	FilterTag reg.Tag
}

// Populate sets the values for GrowOptions.
func (o *GrowOptions) Populate(
	baseDir,
	stagingRepo,
	filterImage,
	filterDigest,
	filterTag string,
) error {
	baseDirAbsPath, err := filepath.Abs(baseDir)
	if err != nil {
		return xerrors.Errorf(
			"cannot resolve %q to absolute path: %w", baseDir, err)
	}

	o.BaseDir = baseDirAbsPath
	o.StagingRepo = reg.RegistryName(stagingRepo)
	o.FilterImage = reg.ImageName(filterImage)
	o.FilterDigest = reg.Digest(filterDigest)
	o.FilterTag = reg.Tag(filterTag)

	return nil
}

// Validate validates the options.
func (o *GrowOptions) Validate() error {
	if o.BaseDir == "" {
		return xerrors.New("must specify --base_dir")
	}

	if o.StagingRepo == "" {
		return xerrors.New("must specify --staging_repo")
	}

	if o.FilterTag == latestTag {
		return xerrors.Errorf(
			"--filter_tag cannot be %q (anti-pattern)", latestTag)
	}
	return nil
}

// Grow modifies a manifest by adding images into it.
func Grow(
	ctx context.Context,
	o *GrowOptions,
) error {
	var err error
	var riiCombined reg.RegInvImage

	// (1) Scan the BaseDir and find the promoter manifest to modify.
	manifest, err := Find(o)
	if err != nil {
		return err
	}

	// (2) Scan the StagingRepo, and whittle the read results down with some
	// filters (Filter* fields in GrowOptions).
	riiUnfiltered, err := ReadStagingRepo(o)
	if err != nil {
		return err
	}

	// (3) Apply some filters.
	riiFiltered, err := ApplyFilters(o, riiUnfiltered)
	if err != nil {
		return err
	}

	// (4) Inject (2)'s output into (1)'s manifest's images to create a larger
	// RegInvImage.
	riiCombined = Union(manifest.ToRegInvImage(), riiFiltered)

	// (5) Write back RegInvImage as Manifest ([]Image field}) back onto disk.
	err = Write(manifest, riiCombined)

	return err
}

// Write writes images as YAML out to the expected path of the given
// (thin) manifest.
func Write(manifest reg.Manifest, rii reg.RegInvImage) error {
	// Chop off trailing "promoter-manifest.yaml".
	p := path.Dir(manifest.Filepath)
	// Get staging repo directory name as it is laid out in the thin manifest
	// dir.
	stagingRepoName := path.Base(p)
	// Construct path to the images.yaml.
	imagesPath := path.Join(p, "..", "..",
		"images", stagingRepoName, "images.yaml")
	logrus.Infoln("RENDER", imagesPath)

	// Write the file.
	err := ioutil.WriteFile(
		imagesPath, []byte(rii.ToYAML(reg.YamlMarshalingOpts{})), 0o644)
	return err
}

// Find finds the manifest to modify.
func Find(o *GrowOptions) (reg.Manifest, error) {
	var err error
	var manifests []reg.Manifest
	manifests, err = reg.ParseThinManifestsFromDir(o.BaseDir)
	if err != nil {
		return reg.Manifest{}, err
	}

	logrus.Infof("%d manifests parsed", len(manifests))
	for _, manifest := range manifests {
		if manifest.SrcRegistry.Name == o.StagingRepo {
			return manifest, nil
		}
	}
	return reg.Manifest{},
		xerrors.Errorf("could not find Manifest for %q", o.StagingRepo)
}

// ReadStagingRepo reads the StagingRepo, and applies whatever filters are
// available to the resulting RegInvImage. This RegInvImage is what we want to
// inject into the "images.yaml" of a thin manifest.
func ReadStagingRepo(
	o *GrowOptions,
) (reg.RegInvImage, error) {
	stagingRepoRC := reg.RegistryContext{
		Name: o.StagingRepo,
	}

	manifests := []reg.Manifest{
		{
			Registries: []reg.RegistryContext{
				stagingRepoRC,
			},
			Images: []reg.Image{},
		},
	}

	sc, err := reg.MakeSyncContext(
		manifests,
		10,
		true,
		false)
	if err != nil {
		return reg.RegInvImage{}, err
	}
	sc.ReadRegistries(
		[]reg.RegistryContext{stagingRepoRC},
		// Read all registries recursively, because we want to produce a
		// complete snapshot.
		true,
		reg.MkReadRepositoryCmdReal)

	return sc.Inv[manifests[0].Registries[0].Name], nil
}

// ApplyFilters applies the filters in the options to whittle down the given
// rii.
func ApplyFilters(o *GrowOptions, rii reg.RegInvImage) (reg.RegInvImage, error) {
	// If nothing to filter, short-circuit.
	if len(rii) == 0 {
		return rii, nil
	}

	// Now perform some filtering, if any.
	if len(o.FilterImage) > 0 {
		rii = FilterByImage(rii, o.FilterImage)
	}

	if len(o.FilterTag) > 0 {
		rii = reg.FilterByTag(rii, string(o.FilterTag))
	}

	if len(o.FilterDigest) > 0 {
		rii = FilterByDigest(rii, o.FilterDigest)
	}

	// Remove any other tags that should still be filtered.
	excludeTags := map[reg.Tag]bool{latestTag: true}
	rii = ExcludeTags(rii, excludeTags)

	if len(rii) == 0 {
		return reg.RegInvImage{}, xerrors.New(
			"no images survived filtering; double-check your --filter_* flag(s) for typos",
		)
	}

	return rii, nil
}

// FilterByImage removes all images in RegInvImage that do not match the
// filterImage.
func FilterByImage(rii reg.RegInvImage, filterImage reg.ImageName) reg.RegInvImage {
	filtered := make(reg.RegInvImage)
	for imageName, digestTags := range rii {
		if imageName == filterImage {
			filtered[imageName] = digestTags
		}
	}
	return filtered
}

// FilterByDigest removes all images in RegInvImage that do not match the
// filterDigest.
func FilterByDigest(rii reg.RegInvImage, filterDigest reg.Digest) reg.RegInvImage {
	filtered := make(reg.RegInvImage)
	for imageName, digestTags := range rii {
		for digest, tags := range digestTags {
			if digest == filterDigest {
				if filtered[imageName] == nil {
					filtered[imageName] = make(reg.DigestTags)
				}
				filtered[imageName][digest] = tags
			}
		}
	}
	return filtered
}

// ExcludeTags removes tags in rii that match excludedTags.
func ExcludeTags(rii reg.RegInvImage, excludedTags map[reg.Tag]bool) reg.RegInvImage {
	filtered := make(reg.RegInvImage)
	for imageName, digestTags := range rii {
		for digest, tags := range digestTags {
			for _, tag := range tags {
				if _, excludeMe := excludedTags[tag]; excludeMe {
					continue
				}
				if filtered[imageName] == nil {
					filtered[imageName] = make(reg.DigestTags)
				}
				filtered[imageName][digest] = append(
					filtered[imageName][digest],
					tag)
			}
		}
	}
	return filtered
}

// Union inject b's contents into a. However, it does so in a special way.
func Union(a, b reg.RegInvImage) reg.RegInvImage {
	for imageName, digestTags := range b {
		// If a does not have this image at all, then it's a simple
		// injection.
		if a[imageName] == nil {
			a[imageName] = digestTags
			continue
		}
		for digest, tags := range digestTags {
			// If a has the image but not this digest, inject just this digest
			// and all associated tags.
			if a[imageName][digest] == nil {
				a[imageName][digest] = tags
				continue
			}
			// If c has the digest already, try to inject those tags in b that
			// are not already in a.
			tagSlice := reg.TagSlice{}
			for tag := range tags.Union(a[imageName][digest]) {
				if tag == "latest" {
					continue
				}
				tagSlice = append(tagSlice, tag)
			}
			a[imageName][digest] = tagSlice
		}
	}

	return a
}
