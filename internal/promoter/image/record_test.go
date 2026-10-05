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

	// recordTestRepoLink is the repository Prow names in the job specification.
	recordTestRepoLink = "https://github.com/kubernetes/k8s.io"
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

func TestRecordInProwCheckout(t *testing.T) {
	// Prow clones the repositories of a job without a remote.
	repo := filepath.Join(t.TempDir(), "src", "github.com", "kubernetes", "k8s.io")
	imagesPath := filepath.Join(repo, "registry.k8s.io", "images", "k8s-staging-foo", "images.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(imagesPath), 0o755))
	require.NoError(t, os.WriteFile(imagesPath, []byte("- name: foo\n"), 0o600))

	git := func(args ...string) string {
		res, err := command.NewWithWorkDir(repo, "git", args...).RunSilentSuccessOutput()
		require.NoError(t, err)

		return res.OutputTrimNL()
	}
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "images")
	commit := git("rev-parse", "HEAD")

	t.Setenv(prowJobSpecEnv, `{"type":"periodic","extra_refs":[{"org":"kubernetes","repo":"k8s.io","repo_link":"https://github.com/kubernetes/k8s.io","base_ref":"main"}]}`)

	rc := newRecordContext([]schema.Manifest{recordTestManifest(t, imagesPath)})
	record := rc.record(recordTestEdges("v1.0.0"), "registry.k8s.io/foo")

	require.Equal(t, "registry.k8s.io/images/k8s-staging-foo/images.yaml", record.GetManifest().GetName())
	require.Equal(t, map[string]string{"gitCommit": commit}, record.GetManifest().GetDigest())
	require.Equal(t, "git+https://github.com/kubernetes/k8s.io", record.GetManifest().GetUri())
}

func TestProwRepositoryLink(t *testing.T) {
	const root = "/home/prow/go/src/github.com/kubernetes/k8s.io"

	for _, tc := range []struct {
		name, spec, root, want string
	}{
		{
			name: "outside of Prow",
			root: root,
		},
		{
			name: "invalid job specification",
			spec: "{",
			root: root,
		},
		{
			name: "postsubmit",
			spec: `{"type":"postsubmit","refs":{"org":"kubernetes","repo":"k8s.io","repo_link":"https://github.com/kubernetes/k8s.io"}}`,
			root: root,
			want: recordTestRepoLink,
		},
		{
			name: "extra refs without a link",
			spec: `{"type":"periodic","extra_refs":[{"org":"kubernetes","repo":"test-infra"},{"org":"kubernetes","repo":"k8s.io"}]}`,
			root: root,
			want: recordTestRepoLink,
		},
		{
			name: "path alias",
			spec: `{"type":"periodic","extra_refs":[{"org":"kubernetes","repo":"k8s.io","repo_link":"https://github.com/kubernetes/k8s.io","path_alias":"k8s.io/k8s.io"}]}`,
			root: "/home/prow/go/src/k8s.io/k8s.io",
			want: recordTestRepoLink,
		},
		{
			name: "repository link outside of GitHub",
			spec: `{"type":"periodic","extra_refs":[{"org":"https://gerrit.example.com","repo":"project","repo_link":"https://gerrit.example.com/project"}]}`,
			root: "/home/prow/go/src/gerrit.example.com/project",
			want: "https://gerrit.example.com/project",
		},
		{
			name: "path alias with a trailing slash",
			spec: `{"type":"periodic","extra_refs":[{"org":"kubernetes","repo":"k8s.io","path_alias":"k8s.io/k8s.io/"}]}`,
			root: "/home/prow/go/src/k8s.io/k8s.io",
			want: recordTestRepoLink,
		},
		{
			name: "presubmit with a merged pull request",
			spec: `{"type":"presubmit","refs":{"org":"kubernetes","repo":"k8s.io","repo_link":"https://github.com/kubernetes/k8s.io","pulls":[{"number":1}]}}`,
			root: root,
		},
		{
			name: "other repository",
			spec: `{"type":"postsubmit","refs":{"org":"kubernetes","repo":"test-infra","repo_link":"https://github.com/kubernetes/test-infra"}}`,
			root: root,
		},
		{
			name: "same repository name in another org",
			spec: `{"type":"postsubmit","refs":{"org":"other","repo":"k8s.io"}}`,
			root: root,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(prowJobSpecEnv, tc.spec)
			require.Equal(t, tc.want, prowRepositoryLink(tc.root))
		})
	}
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
