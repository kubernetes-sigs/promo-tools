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

package inventory_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	reg "sigs.k8s.io/k8s-container-image-promoter/lib/dockerregistry"
)

func TestFindManifest(t *testing.T) {
	pwd := bazelTestPath("TestFindManifest")
	srcRC := reg.RegistryContext{
		Name:           "gcr.io/foo-staging",
		ServiceAccount: "sa@robot.com",
		Src:            true,
	}
	var tests = []struct {
		// name is folder name
		name             string
		input            reg.GrowManifestOptions
		expectedManifest reg.Manifest
		expectedErr      error
	}{
		{
			"empty",
			reg.GrowManifestOptions{
				BaseDir:     filepath.Join(pwd, "empty"),
				StagingRepo: "gcr.io/foo",
			},
			reg.Manifest{},
			&os.PathError{
				Op:   "stat",
				Path: filepath.Join(pwd, "empty/images"),
				Err:  fmt.Errorf("no such file or directory"),
			},
		},
		{
			"singleton",
			reg.GrowManifestOptions{
				BaseDir:     filepath.Join(pwd, "singleton"),
				StagingRepo: "gcr.io/foo-staging",
			},
			reg.Manifest{
				Registries: []reg.RegistryContext{
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
				Images: []reg.Image{
					{ImageName: "foo-controller",
						Dmap: reg.DigestTags{
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
			reg.GrowManifestOptions{
				BaseDir:     filepath.Join(pwd, "singleton"),
				StagingRepo: "gcr.io/nonsense-staging",
			},
			reg.Manifest{},
			fmt.Errorf("could not find Manifest for %q", "gcr.io/nonsense-staging"),
		},
	}

	for _, test := range tests {
		gotManifest, gotErr := reg.FindManifest(test.input)

		// Clean up gotManifest for purposes of comparing against expected
		// results. Namely, clear out the SrcRegistry pointer because this will
		// always be different.
		gotManifest.SrcRegistry = nil

		eqErr := checkEqual(gotManifest, test.expectedManifest)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: %q (unexpected manifest)\n", test.name))

		var gotErrStr string
		var expectedErrStr string
		if gotErr != nil {
			gotErrStr = gotErr.Error()
		}
		if test.expectedErr != nil {
			expectedErrStr = test.expectedErr.Error()
		}

		eqErr = checkEqual(gotErrStr, expectedErrStr)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: %q (unexpected error)\n", test.name))
	}
}
