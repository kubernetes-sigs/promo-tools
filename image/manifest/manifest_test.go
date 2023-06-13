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

package manifest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"sigs.k8s.io/promo-tools/v4/image/manifest"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry/registry"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// TODO: Consider merging this with bazelTestPath() from inventory
func testPath(paths ...string) string {
	prefix := []string{
		os.Getenv("PWD"),
		"testdata",
	}

	return filepath.Join(append(prefix, paths...)...)
}

func TestFind(t *testing.T) {
	pwd := testPath()
	srcRC := registry.Context{
		Name:           "gcr.io/foo-staging",
		ServiceAccount: "sa@robot.com",
		Src:            true,
	}

	tests := []struct {
		// name is folder name
		name             string
		input            manifest.GrowOptions
		expectedManifest schema.Manifest
		expectedErr      error
	}{
		{
			"empty",
			manifest.GrowOptions{
				BaseDir:     filepath.Join(pwd, "empty"),
				StagingRepo: "gcr.io/foo",
			},
			schema.Manifest{},
			&os.PathError{
				Op:   "stat",
				Path: filepath.Join(pwd, "empty/images"),
				Err:  fmt.Errorf("no such file or directory"),
			},
		},
		{
			"singleton",
			manifest.GrowOptions{
				BaseDir:     filepath.Join(pwd, "singleton"),
				StagingRepo: "gcr.io/foo-staging",
			},
			schema.Manifest{
				Registries: []registry.Context{
					srcRC,
					{
						Name:           "us.gcr.io/some-prod",
						ServiceAccount: "sa@robot.com",
					},
					{
						Name:           "eu.gcr.io/some-prod",
						ServiceAccount: "sa@robot.com",
					},
					{
						Name:           "asia.gcr.io/some-prod",
						ServiceAccount: "sa@robot.com",
					},
				},
				Images: []registry.Image{
					{
						Name: "foo-controller",
						Dmap: registry.DigestTags{
							"sha256:c3d310f4741b3642497da8826e0986db5e02afc9777a2b8e668c8e41034128c1": {"1.0"},
						},
					},
				},
				Filepath: filepath.Join(pwd, "singleton/manifests/a/promoter-manifest.yaml"),
			},
			nil,
		},
		{
			"singleton (unrecognized staging repo)",
			manifest.GrowOptions{
				BaseDir:     filepath.Join(pwd, "singleton"),
				StagingRepo: "gcr.io/nonsense-staging",
			},
			schema.Manifest{},
			fmt.Errorf("could not find Manifest for %q", "gcr.io/nonsense-staging"),
		},
	}

	for _, test := range tests {
		gotManifest, gotErr := manifest.Find(&test.input)

		// Clean up gotManifest for purposes of comparing against expected
		// results. Namely, clear out the SrcRegistry pointer because this will
		// always be different.
		gotManifest.SrcRegistry = nil

		require.Equal(t, test.expectedManifest, gotManifest)

		var gotErrStr string
		var expectedErrStr string
		if gotErr != nil {
			gotErrStr = gotErr.Error()
		}
		if test.expectedErr != nil {
			expectedErrStr = test.expectedErr.Error()
		}

		require.Equal(t, expectedErrStr, gotErrStr)
	}
}

func TestApplyFilters(t *testing.T) {
	tests := []struct {
		// name is folder name
		name         string
		inputOptions manifest.GrowOptions
		inputRii     registry.RegInvImage
		expectedRii  registry.RegInvImage
		expectedErr  error
	}{
		{
			"empty rii",
			manifest.GrowOptions{},
			registry.RegInvImage{},
			registry.RegInvImage{},
			nil,
		},
		{
			"no filters --- same as input",
			manifest.GrowOptions{},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"2.0"},
				},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"2.0"},
				},
			},
			nil,
		},
		{
			"remove 'latest' tag by default, even if no filters",
			manifest.GrowOptions{},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"latest", "2.0"},
				},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"2.0"},
				},
			},
			nil,
		},
		{
			"filter on image name only",
			manifest.GrowOptions{
				FilterImages: []image.Name{"bar"},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"latest", "2.0"},
				},
				"bar": {
					"sha256:111": {"latest", "1.0"},
				},
			},
			registry.RegInvImage{
				"bar": {
					"sha256:111": {"1.0"},
				},
			},
			nil,
		},
		{
			"filter on tag only",
			manifest.GrowOptions{
				FilterTags: []image.Tag{"1.0"},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"latest", "2.0"},
				},
				"bar": {
					"sha256:111": {"latest", "1.0"},
				},
			},
			registry.RegInvImage{
				"bar": {
					"sha256:111": {"1.0"},
				},
			},
			nil,
		},
		{
			"filter on 'latest' tag",
			manifest.GrowOptions{
				FilterTags: []image.Tag{"latest"},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"latest", "2.0"},
				},
				"bar": {
					"sha256:111": {"latest", "1.0"},
				},
			},
			registry.RegInvImage{},
			xerrors.New("no images survived filtering; double-check your --filter_* flag(s) for typos"),
		},
		{
			"filter on digest",
			manifest.GrowOptions{
				FilterDigests: []image.Digest{"sha256:222"},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"latest", "2.0"},
					"sha256:222": {"3.0"},
				},
				"bar": {
					"sha256:111": {"latest", "1.0"},
				},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:222": {"3.0"},
				},
			},
			nil,
		},
		{
			"filter on shared tag (multiple images share same tag)",
			manifest.GrowOptions{
				FilterTags: []image.Tag{"1.2.3"},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"latest", "1.2.3"},
					"sha256:222": {"3.0"},
				},
				"bar": {
					"sha256:111": {"latest", "1.2.3"},
				},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"1.2.3"},
				},
				"bar": {
					"sha256:111": {"1.2.3"},
				},
			},
			nil,
		},
		{
			"filter on shared tag and image name (multiple images share same tag)",
			manifest.GrowOptions{
				FilterImages: []image.Name{"foo"},
				FilterTags:   []image.Tag{"1.2.3"},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"latest", "1.2.3"},
					"sha256:222": {"3.0"},
				},
				"bar": {
					"sha256:111": {"latest", "1.2.3"},
				},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:000": {"1.2.3"},
				},
			},
			nil,
		},
	}

	for _, test := range tests {
		gotRii, gotErr := manifest.ApplyFilters(&test.inputOptions, test.inputRii)

		require.Equal(t, test.expectedRii, gotRii)

		if test.expectedErr != nil {
			require.Equal(t, test.expectedErr.Error(), gotErr.Error())
		} else {
			require.Equal(t, test.expectedErr, gotErr)
		}
	}
}
