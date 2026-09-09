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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/image/manifest"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	testServiceAccount = "sa@robot.com"
	imageFoo           = "foo"
	imageBar           = "bar"
	tagLatest          = "latest"
	tag1               = "1.0"
	tag2               = "2.0"
	tag3               = "3.0"
	tag123             = "1.2.3"
	digest000          = "sha256:000"
	digest111          = "sha256:111"
	digest222          = "sha256:222"
)

func testPath(paths ...string) string {
	prefix := make([]string, 0, 2+len(paths))
	prefix = append(prefix, os.Getenv("PWD"), "testdata")

	return filepath.Join(append(prefix, paths...)...)
}

func TestFind(t *testing.T) {
	pwd := testPath()
	srcRC := registry.Context{
		Name:           "gcr.io/foo-staging",
		ServiceAccount: testServiceAccount,
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
				Path: filepath.Join(pwd, "empty", "images"),
				Err:  errors.New("no such file or directory"),
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
						ServiceAccount: testServiceAccount,
					},
					{
						Name:           "eu.gcr.io/some-prod",
						ServiceAccount: testServiceAccount,
					},
					{
						Name:           "asia.gcr.io/some-prod",
						ServiceAccount: testServiceAccount,
					},
				},
				Images: []registry.Image{
					{
						Name: "foo-controller",
						Dmap: registry.DigestTags{
							"sha256:c3d310f4741b3642497da8826e0986db5e02afc9777a2b8e668c8e41034128c1": {tag1},
						},
					},
				},
				Filepath: filepath.Join(pwd, "singleton", "manifests", "a", "promoter-manifest.yaml"),
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

		if test.expectedErr != nil {
			require.ErrorContains(t, gotErr, test.expectedErr.Error())
		} else {
			require.NoError(t, gotErr)
		}
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
				imageFoo: {
					digest000: {tag2},
				},
			},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tag2},
				},
			},
			nil,
		},
		{
			"remove 'latest' tag by default, even if no filters",
			manifest.GrowOptions{},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tagLatest, tag2},
				},
			},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tag2},
				},
			},
			nil,
		},
		{
			"filter on image name only",
			manifest.GrowOptions{
				FilterImages: []image.Name{imageBar},
			},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tagLatest, tag2},
				},
				imageBar: {
					digest111: {tagLatest, tag1},
				},
			},
			registry.RegInvImage{
				imageBar: {
					digest111: {tag1},
				},
			},
			nil,
		},
		{
			"filter on tag only",
			manifest.GrowOptions{
				FilterTags: []image.Tag{tag1},
			},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tagLatest, tag2},
				},
				imageBar: {
					digest111: {tagLatest, tag1},
				},
			},
			registry.RegInvImage{
				imageBar: {
					digest111: {tag1},
				},
			},
			nil,
		},
		{
			"filter on 'latest' tag",
			manifest.GrowOptions{
				FilterTags: []image.Tag{tagLatest},
			},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tagLatest, tag2},
				},
				imageBar: {
					digest111: {tagLatest, tag1},
				},
			},
			registry.RegInvImage{},
			errors.New("no images survived filtering; double-check your --filter_* flag(s) for typos"),
		},
		{
			"filter on digest",
			manifest.GrowOptions{
				FilterDigests: []image.Digest{digest222},
			},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tagLatest, tag2},
					digest222: {tag3},
				},
				imageBar: {
					digest111: {tagLatest, tag1},
				},
			},
			registry.RegInvImage{
				imageFoo: {
					digest222: {tag3},
				},
			},
			nil,
		},
		{
			"filter on shared tag (multiple images share same tag)",
			manifest.GrowOptions{
				FilterTags: []image.Tag{tag123},
			},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tagLatest, tag123},
					digest222: {tag3},
				},
				imageBar: {
					digest111: {tagLatest, tag123},
				},
			},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tag123},
				},
				imageBar: {
					digest111: {tag123},
				},
			},
			nil,
		},
		{
			"filter on shared tag and image name (multiple images share same tag)",
			manifest.GrowOptions{
				FilterImages: []image.Name{imageFoo},
				FilterTags:   []image.Tag{tag123},
			},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tagLatest, tag123},
					digest222: {tag3},
				},
				imageBar: {
					digest111: {tagLatest, tag123},
				},
			},
			registry.RegInvImage{
				imageFoo: {
					digest000: {tag123},
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
