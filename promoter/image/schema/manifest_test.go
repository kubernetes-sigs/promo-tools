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

	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test")

	const (
		repo   = "https://github.com/kubernetes/k8s.io"
		git    = "git"
		commit = "86b8f390aac2e6c244868143ea03c8326c9064a0"
	)

	require.NoError(t, command.New(git, "clone", repo, testDir).RunSilentSuccess())
	require.NoError(t, command.NewWithWorkDir(testDir, git, "checkout", commit).RunSilentSuccess())

	for _, onlyProwDiff := range []bool{true, false} {
		manifests, err := ParseThinManifestsFromDir(
			filepath.Join(testDir, "k8s.gcr.io"), onlyProwDiff, "",
		)

		require.NoError(t, err)
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

func TestDiffSinceFiles(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	const git = "git"

	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "init").RunSilentSuccess())
	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "config", "user.email", "test@test.com").RunSilentSuccess())
	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "config", "user.name", "test").RunSilentSuccess())
	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "config", "commit.gpgsign", "false").RunSilentSuccess())

	imagesDir := filepath.Join(tmpDir, "images", "test")
	require.NoError(t, os.MkdirAll(imagesDir, 0o755))

	imagesFile := filepath.Join(imagesDir, "images.yaml")
	require.NoError(t, os.WriteFile(imagesFile, []byte("initial\n"), 0o600))

	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "add", ".").RunSilentSuccess())

	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "commit", "-m", "initial",
		"--date", "2020-01-01T00:00:00Z").
		Env("GIT_COMMITTER_DATE=2020-01-01T00:00:00Z").
		RunSilentSuccess())

	contentA := `- name: test-image
  dmap:
    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": ["v1.0"]
`
	require.NoError(t, os.WriteFile(imagesFile, []byte(contentA), 0o600))

	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "add", ".").RunSilentSuccess())
	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "commit", "-m", "add digest A").RunSilentSuccess())

	contentB := `- name: test-image
  dmap:
    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": ["v1.0"]
    "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": ["v2.0"]
`
	require.NoError(t, os.WriteFile(imagesFile, []byte(contentB), 0o600))

	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "add", ".").RunSilentSuccess())
	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "commit", "-m", "add digest B").RunSilentSuccess())

	digests, err := diffSinceFiles(tmpDir, "1 day")
	require.NoError(t, err)
	assert.Len(t, digests, 2)
	assert.Contains(t, digests, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	assert.Contains(t, digests, "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
}

func TestDiffSinceFilesNoChanges(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	const git = "git"

	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "init").RunSilentSuccess())
	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "config", "user.email", "test@test.com").RunSilentSuccess())
	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "config", "user.name", "test").RunSilentSuccess())
	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "config", "commit.gpgsign", "false").RunSilentSuccess())

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("test\n"), 0o600))

	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "add", ".").RunSilentSuccess())
	require.NoError(t, command.NewWithWorkDir(tmpDir, git, "commit", "-m", "initial").RunSilentSuccess())

	digests, err := diffSinceFiles(tmpDir, "1 second")
	require.NoError(t, err)
	assert.Empty(t, digests)
}
