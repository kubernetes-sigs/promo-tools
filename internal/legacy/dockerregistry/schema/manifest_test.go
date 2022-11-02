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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/release-utils/command"
)

func TestParseThinManifestsFromDirPostsubmit(t *testing.T) {
	t.Setenv("JOB_TYPE", "postsubmit")

	tmpDir, err := os.MkdirTemp("", "k8s.io-")
	require.Nil(t, err)
	testDir := filepath.Join(tmpDir, "test")
	defer os.RemoveAll(tmpDir)

	const (
		repo   = "https://github.com/kubernetes/k8s.io"
		git    = "git"
		commit = "86b8f390aac2e6c244868143ea03c8326c9064a0"
	)

	require.Nil(t, command.New(git, "clone", repo, testDir).RunSilentSuccess())
	require.Nil(t, command.NewWithWorkDir(testDir, git, "checkout", commit).RunSilentSuccess())

	for _, onlyProwDiff := range []bool{true, false} {
		manifests, err := ParseThinManifestsFromDir(
			filepath.Join(testDir, "k8s.gcr.io"), onlyProwDiff,
		)

		require.Nil(t, err)
		require.Len(t, manifests, 76)

		var digestCount, imageCount int
		for _, manifest := range manifests {
			imageCount += len(manifest.Images)
			for _, image := range manifest.Images {
				digestCount += len(image.Dmap)
			}
		}

		expectedDigestCount := 12344
		if onlyProwDiff {
			expectedDigestCount = 1
		}
		assert.Equal(t, expectedDigestCount, digestCount)
		assert.Equal(t, 623, imageCount)
	}
}
