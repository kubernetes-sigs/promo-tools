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

package promobot_test

import (
	"testing"

	"sigs.k8s.io/promo-tools/v4/promobot"
	"sigs.k8s.io/yaml"
)

func TestReadManifests(t *testing.T) {
	grid := []struct {
		Expected string
		Options  promobot.PromoteFilesOptions
	}{
		{
			Expected: "testdata/expected/onefile.yaml",
			Options: promobot.PromoteFilesOptions{
				FilestoresPath: "testdata/manifests/onefile/filepromoter-manifest.yaml",
				FilesPath:      "testdata/manifests/onefile/files.yaml",
			},
		},
		{
			Expected: "testdata/expected/manyfiles.yaml",
			Options: promobot.PromoteFilesOptions{
				FilestoresPath: "testdata/manifests/manyfiles/filepromoter-manifest.yaml",
				FilesPath:      "testdata/manifests/manyfiles/files/",
			},
		},
		{
			Expected: "testdata/expected/manyprojects.yaml",
			Options: promobot.PromoteFilesOptions{
				ManifestsPath: "testdata/manifests/manyprojects/",
			},
		},
	}

	for _, g := range grid {
		t.Run(g.Expected, func(t *testing.T) {
			manifests, err := promobot.ReadManifests(g.Options)
			if err != nil {
				t.Fatalf("failed to read manifests: %v", err)
			}

			manifestYAML, err := yaml.Marshal(manifests)
			if err != nil {
				t.Fatalf("error serializing manifest: %v", err)
			}

			AssertMatchesFile(t, string(manifestYAML), g.Expected)
		})
	}
}
