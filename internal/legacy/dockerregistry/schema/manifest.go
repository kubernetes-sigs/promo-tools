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

package schema

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"

	"sigs.k8s.io/promo-tools/v3/internal/legacy/dockerregistry/registry"
	"sigs.k8s.io/promo-tools/v3/types/image"
)

// Manifest stores the information in a manifest file (describing the
// desired state of a Docker Registry).
type Manifest struct {
	// Registries contains the source and destination (Src/Dest) registry names.
	// There must be at least 2 registries: 1 source registry and 1 or more
	// destination registries.
	Registries []registry.Context `yaml:"registries,omitempty"`
	Images     []registry.Image   `yaml:"images,omitempty"`

	// Hidden fields; these are data structure optimizations that are populated
	// from the fields above. As they are redundant, there is no point in
	// storing this information in YAML.
	SrcRegistry *registry.Context
	Filepath    string
}

// ThinManifest is a more secure Manifest because it does not define the
// Images[] directly, but moves it to a separate location. The idea is to define
// a ThinManifest type as a YAML in one folder, and to define the []Image in
// another folder, and to have far stricter ACLs for the ThinManifest type.
// Then, PRs modifying just the []Image YAML won't be able to modify the
// src/destination repos or the credentials tied to them.
type ThinManifest struct {
	Registries []registry.Context `yaml:"registries,omitempty"`
	// Store actual image data somewhere else.
	//
	// NOTE: "ImagesPath" is deprecated. It does nothing and will be
	// removed in a future release. The images are always stored in a
	// directory structure as follows:
	//
	//       foo
	//       ├── images
	//       │   ├── a
	//       │   │   └── images.yaml
	//       │   ├── b
	//       │   │   └── images.yaml
	//       │   ├── c
	//       │   │   └── images.yaml
	//       │   └── d
	//       │       └── images.yaml
	//       └── manifests
	//           ├── a
	//           │   └── promoter-manifest.yaml
	//           ├── b
	//           │   └── promoter-manifest.yaml
	//           ├── c
	//           │   └── promoter-manifest.yaml
	//           └── d
	//               └── promoter-manifest.yaml
	//
	// where "foo" is the toplevel folder holding all thin manifsets.
	// That is, every manifest must be bifurcated into 2 parts, the
	// "image" and "manifest" part, and these parts must be stored
	// separately.

	ImagesPath string `yaml:"imagesPath,omitempty"`
}

const (
	// ThinManifestDepth specifies the number of items in a path if we split the
	// path into its parts, starting from the "topmost" folder given as an
	// argument to -thin-manifest-dir. E.g., a well-formed path is something
	// like:
	//
	//  ["", "manifests", "foo", "promoter-manifests.yaml"]
	//
	// . This is a result of some path handling/parsing logic in
	// ValidateThinManifestDirectoryStructure().
	ThinManifestDepth = 4
)

// Validate checks for semantic errors in the yaml fields (the structure of the
// yaml is checked during unmarshaling).
func (m Manifest) Validate() error {
	if err := validateRequiredComponents(m); err != nil {
		return err
	}
	return validateImages(m.Images)
}

func validateRequiredComponents(m Manifest) error {
	// TODO: Should we return []error here instead?
	errs := make([]string, 0)
	srcRegistryName := image.Registry("")

	if len(m.Registries) > 0 {
		if m.srcRegistryCount() > 1 {
			errs = append(errs, "cannot have more than 1 source registry")
		}

		srcRegistryName = m.srcRegistryName()
		if len(srcRegistryName) == 0 {
			errs = append(errs, "source registry must be set")
		}
	}

	knownRegistries := make([]image.Registry, 0)
	if len(m.Registries) == 0 {
		errs = append(errs, "'registries' field cannot be empty")
	}

	for _, registry := range m.Registries {
		if len(registry.Name) == 0 {
			errs = append(
				errs,
				"registries: 'name' field cannot be empty",
			)
		}

		// TODO(lint): SA4010: this result of append is never used, except maybe in other appends
		//nolint:staticcheck
		knownRegistries = append(knownRegistries, registry.Name)
	}

	for _, img := range m.Images {
		if len(img.Name) == 0 {
			errs = append(
				errs,
				"images: 'name' field cannot be empty",
			)
		}

		if len(img.Dmap) == 0 {
			errs = append(
				errs,
				"images: 'dmap' field cannot be empty",
			)
		}
	}

	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf(strings.Join(errs, "\n"))
}

func (m Manifest) srcRegistryCount() int {
	var count int
	for _, registry := range m.Registries {
		if registry.Src {
			count++
		}
	}

	return count
}

func (m Manifest) srcRegistryName() image.Registry {
	for _, registry := range m.Registries {
		if registry.Src {
			return registry.Name
		}
	}

	return image.Registry("")
}

func validateImages(images []registry.Image) error {
	for _, image := range images {
		for digest, tagSlice := range image.Dmap {
			if err := ValidateDigest(digest); err != nil {
				return err
			}

			for _, tag := range tagSlice {
				if err := ValidateTag(tag); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ValidateDigest validates the digest.
func ValidateDigest(digest image.Digest) error {
	validDigest := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	if !validDigest.Match([]byte(digest)) {
		return fmt.Errorf("invalid digest: %v", digest)
	}

	return nil
}

// ValidateTag validates the tag.
func ValidateTag(tag image.Tag) error {
	validTag := regexp.MustCompile(`^[\w][\w.-]{0,127}$`)
	if !validTag.Match([]byte(tag)) {
		return fmt.Errorf("invalid tag: %v", tag)
	}

	return nil
}

// Finalize finalizes a Manifest by populating extra fields.
func (m *Manifest) Finalize() error {
	// Perform semantic checks (beyond just YAML validation).
	srcRegistry, err := registry.GetSrcRegistry(m.Registries)
	if err != nil {
		return err
	}
	m.SrcRegistry = srcRegistry

	return nil
}

// ToRegInvImage converts a Manifest into a RegInvImage.
func (m *Manifest) ToRegInvImage() registry.RegInvImage {
	rii := make(registry.RegInvImage)
	for _, img := range m.Images {
		rii[img.Name] = img.Dmap
	}
	return rii
}

// Parsers

// ParseThinManifestsFromDir parses all thin Manifest files within a directory.
// We effectively have to create a map of manifests, keyed by the source
// registry (there can only be 1 source registry).
func ParseThinManifestsFromDir(
	dir string,
) ([]Manifest, error) {
	mfests := make([]Manifest, 0)

	// Check that the thin manifests dir follows a regular, predefined format.
	// This is to ensure that there isn't any funny business going on around
	// paths.
	if err := ValidateThinManifestDirectoryStructure(dir); err != nil {
		return mfests, err
	}

	var parseAsManifest filepath.WalkFunc = func(
		path string,
		info os.FileInfo,
		err error,
	) error {
		if err != nil {
			// Prevent panic in case of incoming errors accessing this path.
			logrus.Errorf("failure accessing a path %q: %v\n", path, err)
		}

		// Skip directories (because they are not YAML files).
		if info.IsDir() {
			return nil
		}

		// First try to parse the path as a manifest file, which must be named
		// "promoter-manifest.yaml". This restriction is in place to limit the
		// scope of what is read in as a promoter manifest.
		if filepath.Base(path) != "promoter-manifest.yaml" {
			return nil
		}

		// If there are any files named "promoter-manifest.yaml", they must be
		// inside a subfolder within "manifests/<dir>" --- any other paths are
		// forbidden.
		shortened := strings.TrimPrefix(path, dir)
		shortenedList := strings.Split(shortened, "/")
		if len(shortenedList) != ThinManifestDepth {
			return fmt.Errorf("unexpected manifest path %q",
				path)
		}

		mfest, errParse := ParseThinManifestFromFile(path)
		if errParse != nil {
			logrus.Errorf("could not parse manifest file '%s'\n", path)
			return errParse
		}

		// Save successful parse result.
		mfests = append(mfests, mfest)

		return nil
	}

	// Only look at manifests starting with the "manifests" subfolder (no need
	// to walk any other toplevel subfolder).
	if err := filepath.Walk(filepath.Join(dir, "manifests"), parseAsManifest); err != nil {
		return mfests, err
	}

	if len(mfests) == 0 {
		return nil, fmt.Errorf("no manifests found in dir: %s", dir)
	}

	return mfests, nil
}

// ParseManifestFromFile parses a Manifest from a filepath.
func ParseManifestFromFile(filePath string) (Manifest, error) {
	var mfest Manifest
	var empty Manifest

	b, err := ioutil.ReadFile(filePath)
	if err != nil {
		return empty, err
	}

	mfest, err = ParseManifestYAML(b)
	if err != nil {
		return empty, err
	}

	mfest.Filepath = filePath

	err = mfest.Finalize()
	if err != nil {
		return empty, err
	}

	return mfest, nil
}

// ParseThinManifestFromFile parses a ThinManifest from a filepath and generates
// a Manifest.
func ParseThinManifestFromFile(filePath string) (Manifest, error) {
	var thinManifest ThinManifest
	var mfest Manifest
	var empty Manifest

	b, err := ioutil.ReadFile(filePath)
	if err != nil {
		return empty, err
	}

	thinManifest, err = ParseThinManifestYAML(b)
	if err != nil {
		return empty, err
	}

	// Get directory name holding this thin manifest.
	subProject := filepath.Base(filepath.Dir(filePath))
	imagesPath := filepath.Join(filepath.Dir(filePath),
		"../../images",
		subProject,
		"images.yaml")
	images, err := ParseImagesFromFile(imagesPath)
	if err != nil {
		return empty, err
	}

	mfest.Filepath = filePath
	mfest.Images = images
	mfest.Registries = thinManifest.Registries

	err = mfest.Finalize()
	if err != nil {
		return empty, err
	}

	return mfest, nil
}

// ParseManifestYAML parses a Manifest from a byteslice. This function is
// separate from ParseManifestFromFile() so that it can be tested independently.
func ParseManifestYAML(b []byte) (Manifest, error) {
	var m Manifest
	if err := yaml.UnmarshalStrict(b, &m); err != nil {
		return m, err
	}

	return m, m.Validate()
}

// ParseThinManifestYAML parses a ThinManifest from a byteslice.
func ParseThinManifestYAML(b []byte) (ThinManifest, error) {
	var m ThinManifest
	if err := yaml.UnmarshalStrict(b, &m); err != nil {
		return m, err
	}

	return m, nil
}

// ParseImagesYAML parses Images from a byteslice.
func ParseImagesYAML(b []byte) (registry.Images, error) {
	var images registry.Images
	if err := yaml.UnmarshalStrict(b, &images); err != nil {
		return images, err
	}

	return images, nil
}

// ValidateThinManifestDirectoryStructure enforces a particular directory
// structure for thin manifests. Most importantly, it requires that if a file
// named "foo/manifests/bar/promoter-manifest.yaml" exists, that a corresponding
// file named "foo/images/bar/promoter-manifest.yaml" must also exist.
func ValidateThinManifestDirectoryStructure(
	dir string,
) error {
	// First, enforce that there are directories named "images" and "manifests".
	if err := validateIsDirectory(filepath.Join(dir, "images")); err != nil {
		return err
	}

	manifestDir := filepath.Join(dir, "manifests")
	if err := validateIsDirectory(manifestDir); err != nil {
		return err
	}

	// For every subfolder in <dir>/manifests, ensure that a
	// "promoter-manifest.yaml" file exists, and also that a corresponding file
	// exists in the "images" folder.
	files, err := ioutil.ReadDir(manifestDir)
	if err != nil {
		return err
	}

	logrus.Infof("*looking at %q", dir)
	for _, file := range files {
		p, err := os.Stat(filepath.Join(manifestDir, file.Name()))
		if err != nil {
			return err
		}

		// Skip non-directory sub-paths.
		if !p.IsDir() {
			continue
		}

		// Search for a "promoter-manifest.yaml" file under this directory.
		manifestInfo, err := os.Stat(
			filepath.Join(manifestDir,
				file.Name(),
				"promoter-manifest.yaml"))
		if err != nil {
			logrus.Warningln(err)
			continue
		}
		if !manifestInfo.Mode().IsRegular() {
			logrus.Warnf("ignoring irregular file %q", manifestInfo)
			continue
		}

		// "promoter-manifest.yaml" exists, so check for corresponding images
		// file, which MUST exist. This is why we fail early if we detect an
		// error here.
		imagesPath := filepath.Join(dir,
			"images",
			file.Name(),
			"images.yaml")
		imagesInfo, err := os.Stat(imagesPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("corresponding file %q does not exist",
					imagesPath)
			}
			return err
		}

		if !imagesInfo.Mode().IsRegular() {
			return fmt.Errorf("corresponding file %q is not a regular file",
				imagesPath)
		}
	}

	return nil
}

// ParseImagesFromFile parses an Images type from a file.
func ParseImagesFromFile(filePath string) (registry.Images, error) {
	var images registry.Images
	var empty registry.Images

	b, err := ioutil.ReadFile(filePath)
	if err != nil {
		return empty, err
	}

	images, err = ParseImagesYAML(b)
	if err != nil {
		return empty, err
	}

	return images, nil
}

// validateIsDirectory returns nil if it does exist, otherwise a non-nil error.
func validateIsDirectory(dir string) error {
	p, err := os.Stat(filepath.Join(dir))
	if err != nil {
		return err
	}
	if !p.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}
	return nil
}
