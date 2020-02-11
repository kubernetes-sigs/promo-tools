/*
Copyright 2019 The Kubernetes Authors.

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

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"
	api "sigs.k8s.io/k8s-container-image-promoter/pkg/api/files"
	"sigs.k8s.io/k8s-container-image-promoter/pkg/filepromoter"
)

// HashFilesOptions holds the flag-values for a hash-files operation
type HashFilesOptions struct {
	// BaseDir is the directory containing the files to hash
	BaseDir string
}

// HashFilesOptions sets the default values for HashFilesOptions
func (o *HashFilesOptions) PopulateDefaults() {
}

// RunHashFiles executes a file promotion command
func RunHashFiles(ctx context.Context, options HashFilesOptions) (*api.Manifest, error) {
	manifest := &api.Manifest{}

	if options.BaseDir == "" {
		return nil, xerrors.New("must specify BaseDir")
	}

	basedir := options.BaseDir
	if !strings.HasSuffix(basedir, "/") {
		basedir += "/"
	}
	if err := filepath.Walk(basedir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasPrefix(p, basedir) {
			return xerrors.Errorf("expected path %q to have prefix %q", p, basedir)
		}

		if !info.IsDir() {
			relativePath := strings.TrimPrefix(p, basedir)
			sha256, err := filepromoter.ComputeSHA256ForFile(p)
			if err != nil {
				return xerrors.Errorf("error hashing file %q: %w", p, err)
			}
			manifest.Files = append(manifest.Files, api.File{
				Name:   relativePath,
				SHA256: sha256,
			})
		}
		return nil
	}); err != nil {
		return nil, xerrors.Errorf("error walking path %q: %w", options.BaseDir, err)
	}

	return manifest, nil
}
