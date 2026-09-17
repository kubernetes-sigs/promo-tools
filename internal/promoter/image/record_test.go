/*
Copyright 2026 The Kubernetes Authors.

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

package imagepromoter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/release-utils/command"

	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	recordTestStaging = "gcr.io/k8s-staging-foo"
	recordTestImage   = "foo"
	recordTestDigest  = "sha256"
)

func recordTestEdges(tags ...image.Tag) []promotion.Edge {
	edges := make([]promotion.Edge, 0, len(tags))

	for _, tag := range tags {
		edges = append(edges, promotion.Edge{
			SrcRegistry: registry.Context{Name: recordTestStaging, Src: true},
			SrcImageTag: promotion.ImageTag{Name: recordTestImage, Tag: tag},
			Digest:      testDigest,
			DstRegistry: registry.Context{Name: "us-central1-docker.pkg.dev/k8s-artifacts-prod/images/foo"},
			DstImageTag: promotion.ImageTag{Name: recordTestImage, Tag: tag},
		})
	}

	return edges
}

func recordTestManifest(t *testing.T, imagesPath string) schema.Manifest {
	t.Helper()

	src := registry.Context{Name: recordTestStaging, Src: true}

	return schema.Manifest{
		Registries:     []registry.Context{src},
		Images:         []registry.Image{{Name: recordTestImage, Dmap: registry.DigestTags{testDigest: {"v1.0.0"}}}},
		SrcRegistry:    &src,
		Filepath:       filepath.Join(filepath.Dir(imagesPath), "promoter-manifest.yaml"),
		ImagesFilepath: imagesPath,
	}
}

func TestRecordWithProwJob(t *testing.T) {
	t.Setenv(prowJobNameEnv, "post-k8sio-image-promo")
	t.Setenv(prowJobTypeEnv, "postsubmit")
	t.Setenv(prowBuildIDEnv, "1234")
	t.Setenv(prowProwJobIDEnv, "abcd-ef")

	repo := t.TempDir()
	imagesPath := filepath.Join(repo, "registry.k8s.io", "images", "k8s-staging-foo", "images.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(imagesPath), 0o755))
	require.NoError(t, os.WriteFile(imagesPath, []byte("- name: foo\n"), 0o600))

	git := func(args ...string) string {
		res, err := command.NewWithWorkDir(repo, "git", args...).RunSilentSuccessOutput()
		require.NoError(t, err)

		return res.OutputTrimNL()
	}
	git("init", "-q")
	git("remote", "add", "origin", "https://user:secret@github.com/kubernetes/k8s.io")
	git("add", ".")
	git("-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "images")
	commit := git("rev-parse", "HEAD")

	rc := newRecordContext([]schema.Manifest{recordTestManifest(t, imagesPath)})
	record := rc.record(recordTestEdges("v1.0.0", "latest", "v1.0.0"), "registry.k8s.io/foo")

	require.Equal(t, []string{"latest", "v1.0.0"}, record.GetTags())
	require.Equal(t, recordTestStaging+"/foo", record.GetSource().GetName())
	require.Equal(t, map[string]string{recordTestDigest: testDigest[len(recordTestDigest+":"):]}, record.GetSource().GetDigest())
	require.Equal(t, "registry.k8s.io/foo", record.GetDestination().GetName())
	require.Equal(t, record.GetSource().GetDigest(), record.GetDestination().GetDigest())

	require.Equal(t, "registry.k8s.io/images/k8s-staging-foo/images.yaml", record.GetManifest().GetName())
	require.Equal(t, map[string]string{"gitCommit": commit}, record.GetManifest().GetDigest())
	require.Equal(t, "git+https://github.com/kubernetes/k8s.io", record.GetManifest().GetUri())

	require.Equal(t, &provenance.Job{
		Name:      "post-k8sio-image-promo",
		Type:      "postsubmit",
		BuildId:   "1234",
		ProwJobId: "abcd-ef",
	}, record.GetJob())
	require.NotNil(t, record.GetPromoter())

	// The new fields end up in the predicate.
	stmt, err := (&provenance.PromotionGenerator{}).Generate(context.Background(), record)
	require.NoError(t, err)

	var parsed struct {
		Predicate map[string]any `json:"predicate"`
	}
	require.NoError(t, json.Unmarshal(stmt, &parsed))

	for _, field := range []string{"tags", "source", "destination", "manifest", "promoter", "job"} {
		require.Contains(t, parsed.Predicate, field)
	}
}

func TestRecordWithoutProwOrManifest(t *testing.T) {
	t.Setenv(prowJobNameEnv, "")

	// A manifest outside of a git repository only records its path.
	imagesPath := filepath.Join(t.TempDir(), "images.yaml")
	rc := newRecordContext([]schema.Manifest{recordTestManifest(t, imagesPath)})

	// Descriptors are only built for manifests of promoted edges.
	require.Empty(t, rc.descriptors)

	record := rc.record(recordTestEdges("v1.0.0"), "registry.k8s.io/foo")
	require.Len(t, rc.descriptors, 1)
	require.Nil(t, record.GetJob())
	require.Equal(t, imagesPath, record.GetManifest().GetName())
	require.Empty(t, record.GetManifest().GetDigest())
	require.Empty(t, record.GetManifest().GetUri())

	// Edges without a manifest and tagless edges leave those fields empty.
	rc = newRecordContext(nil)
	record = rc.record(recordTestEdges(""), "registry.k8s.io/foo")
	require.Nil(t, record.GetManifest())
	require.Empty(t, record.GetTags())
	require.Equal(t, "registry.k8s.io/foo", record.GetDestination().GetName())
}

func TestRepositoryURI(t *testing.T) {
	t.Parallel()

	for remote, want := range map[string]string{ //nolint:gosec // remotes with fake credentials
		"https://github.com/kubernetes/k8s.io":          "git+https://github.com/kubernetes/k8s.io",
		"https://user:password@github.com/org/repo.git": "git+https://github.com/org/repo.git",
		"git@github.com:kubernetes/k8s.io.git":          "",
		"/local/path":                                   "",
		"":                                              "",
	} {
		require.Equal(t, want, repositoryURI(remote), remote)
	}
}
