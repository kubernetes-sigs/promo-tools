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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/release-utils/command"

	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/types/image"
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

func TestParseThinManifestsFromDirPath(t *testing.T) {
	tmpDir := t.TempDir()

	manifestDir := filepath.Join(tmpDir, "manifests", "test")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "promoter-manifest.yaml"), []byte(`registries:
- name: gcr.io/k8s-staging-test
  src: true
- name: us-central1-docker.pkg.dev/k8s-artifacts-prod/images/test
  service-account: k8s-infra-gcr-promoter@k8s-artifacts-prod.iam.gserviceaccount.com
`), 0o600))

	imagesDir := filepath.Join(tmpDir, "images", "test")
	require.NoError(t, os.MkdirAll(imagesDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(imagesDir, "images.yaml"), []byte(`- name: test-image
  dmap:
    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": ["v1.0"]
`), 0o600))

	t.Chdir(tmpDir)

	for _, dir := range []string{
		tmpDir,
		tmpDir + string(filepath.Separator),
		".",
		"." + string(filepath.Separator),
		filepath.Join("..", filepath.Base(tmpDir)),
	} {
		t.Run(dir, func(t *testing.T) {
			manifests, err := ParseThinManifestsFromDir(dir, false, "")
			require.NoError(t, err)
			require.Len(t, manifests, 1)
			require.Len(t, manifests[0].Images, 1)
		})
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

const testPolicyYAML = `registries:
- name: us-central1-docker.pkg.dev/k8s-staging-images/sp-operator
  src: true
- name: us-central1-docker.pkg.dev/k8s-artifacts-prod/images/security-profiles-operator
provenance:
  mode: require
  signers:
  - sigstore::https://accounts.google.com::sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com
  builders:
  - id: https://prow.k8s.io/post-security-profiles-operator-push-image
    level: 3
  sources:
  - github.com/kubernetes-sigs/security-profiles-operator
  predicateTypes:
  - https://spdx.dev/Document
  level: 2
`

func TestParseThinManifestYAMLProvenanceBuilderWithoutLevel(t *testing.T) {
	t.Parallel()

	// Builders were plain IDs before they had a level.
	yaml := strings.Replace(testPolicyYAML,
		"  - id: https://prow.k8s.io/post-security-profiles-operator-push-image\n    level: 3\n",
		"  - https://prow.k8s.io/post-security-profiles-operator-push-image\n", 1)
	require.NotEqual(t, testPolicyYAML, yaml)

	_, err := ParseThinManifestYAML([]byte(yaml))
	require.ErrorContains(t, err, `builder "https://prow.k8s.io/post-security-profiles-operator-push-image" needs a level`)
}

func TestParseThinManifestYAMLProvenance(t *testing.T) {
	t.Parallel()

	m, err := ParseThinManifestYAML([]byte(testPolicyYAML))
	require.NoError(t, err)
	require.Equal(t, &provenance.Policy{
		Mode:           provenance.PolicyModeRequire,
		Signers:        []string{"sigstore::https://accounts.google.com::sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com"},
		Builders:       []provenance.Builder{{ID: "https://prow.k8s.io/post-security-profiles-operator-push-image", Level: 3}},
		Sources:        []string{"github.com/kubernetes-sigs/security-profiles-operator"},
		PredicateTypes: []string{"https://spdx.dev/Document"},
		Level:          2,
	}, m.Provenance)

	// Without a policy section, the manifest has no policy.
	m, err = ParseThinManifestYAML([]byte("registries:\n- name: gcr.io/staging\n  src: true\n"))
	require.NoError(t, err)
	require.Nil(t, m.Provenance)
}

// testCallersYAML adds two alternative callers to the builder of
// testPolicyYAML.
const testCallersYAML = "    callers:\n" +
	"    - workflows:\n      - .github/workflows/build.yml\n      - .github/workflows/image-reproducible.yml\n" +
	"      refs:\n      - refs/tags/v*\n      events:\n      - release\n" +
	"    - workflows:\n      - .github/workflows/build.yml\n" +
	"      refs:\n      - refs/heads/main\n      events:\n      - push\n"

// withCallers returns testPolicyYAML with the callers YAML on its builder,
// which it moves to github.com, since callers need a builder there.
func withCallers(callers string) string {
	return strings.Replace(testPolicyYAML,
		"  - id: https://prow.k8s.io/post-security-profiles-operator-push-image\n    level: 3\n",
		"  - id: https://github.com/kubernetes-sigs/security-profiles-operator/.github/workflows/provenance.yml\n"+
			"    level: 3\n"+callers, 1)
}

func TestParseThinManifestYAMLProvenanceCallers(t *testing.T) {
	t.Parallel()

	yaml := withCallers(testCallersYAML)
	require.NotEqual(t, testPolicyYAML, yaml)

	m, err := ParseThinManifestYAML([]byte(yaml))
	require.NoError(t, err)
	require.Equal(t, []provenance.Caller{
		{
			Workflows: []string{".github/workflows/build.yml", ".github/workflows/image-reproducible.yml"},
			Refs:      []string{"refs/tags/v*"},
			Events:    []string{"release"},
		},
		{
			Workflows: []string{".github/workflows/build.yml"},
			Refs:      []string{"refs/heads/main"},
			Events:    []string{"push"},
		},
	}, m.Provenance.Builders[0].Callers)
}

func TestParseThinManifestYAMLProvenanceInvalid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "unknown mode",
			yaml:    strings.Replace(testPolicyYAML, "mode: require", "mode: enforce", 1),
			wantErr: `mode must be "off", "warn" or "require"`,
		},
		{
			name:    "unknown field",
			yaml:    testPolicyYAML + "  identities: []\n",
			wantErr: "field identities not found",
		},
		{
			name:    "invalid signer",
			yaml:    strings.Replace(testPolicyYAML, "sigstore::https", "unknown::https", 1),
			wantErr: "invalid signer",
		},
		{
			name:    "invalid caller event",
			yaml:    withCallers(testCallersYAML + "    - refs:\n      - refs/tags/v*\n      events:\n      - Release\n"),
			wantErr: `callers[2]: event "Release" must be a GitHub Actions event name`,
		},
		{
			name:    "unknown caller field",
			yaml:    withCallers(testCallersYAML + "      repositories: []\n"),
			wantErr: "field repositories not found",
		},
		{
			name:    "single caller",
			yaml:    withCallers("    callers:\n      refs:\n      - refs/tags/v*\n"),
			wantErr: "cannot unmarshal",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseThinManifestYAML([]byte(tc.yaml))
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestParseManifestYAMLProvenanceInvalid(t *testing.T) {
	t.Parallel()

	_, err := ParseManifestYAML([]byte(strings.Replace(testPolicyYAML, "mode: require", "mode: enforce", 1)))
	require.ErrorContains(t, err, "validating provenance policy")
}

func TestParseThinManifestFromFileProvenance(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifests", "sp-operator", "promoter-manifest.yaml")
	imagesPath := filepath.Join(dir, "images", "sp-operator", "images.yaml")

	require.NoError(t, os.MkdirAll(filepath.Dir(manifestPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(imagesPath), 0o755))
	require.NoError(t, os.WriteFile(manifestPath, []byte(testPolicyYAML), 0o600))
	require.NoError(t, os.WriteFile(imagesPath, []byte("[]\n"), 0o600))

	m, err := ParseThinManifestFromFile(manifestPath, nil)
	require.NoError(t, err)
	require.True(t, m.Provenance.Enabled())
	require.Equal(t, provenance.PolicyModeRequire, m.Provenance.Mode)
}

// testPolicySrc and testPolicyImage name a source registry with a provenance
// policy and an image in it.
const (
	testPolicySrc   = "gcr.io/a"
	testPolicyImage = "img"
)

// testNestedSrc is a source registry nested in testPolicySrc, testScopeImage
// and testScopePattern an image and a pattern of the images of a policy, and
// testTypoPattern a pattern that matches no image.
const (
	testNestedSrc    = "gcr.io/a/nested"
	testScopeImage   = "kube-apiserver"
	testScopePattern = "kube-*"
	testTypoPattern  = "typo"
)

func TestProvenancePolicies(t *testing.T) {
	t.Parallel()

	requirePolicy := &provenance.Policy{
		Mode:     provenance.PolicyModeRequire,
		Signers:  []string{"sigstore::https://accounts.google.com::a@example.com"},
		Builders: []provenance.Builder{{ID: "https://builder.example.com", Level: 3}},
		Sources:  []string{"github.com/example/a"},
	}
	warn := &provenance.Policy{
		Mode:     provenance.PolicyModeWarn,
		Signers:  []string{"sigstore::https://accounts.google.com::a@example.com"},
		Builders: []provenance.Builder{{ID: "https://builder.example.com", Level: 3}},
		Sources:  []string{"github.com/example/a"},
	}

	manifest := func(src string, policy *provenance.Policy) Manifest {
		return Manifest{
			Registries: []registry.Context{
				{Name: image.Registry(src), Src: true},
				{Name: "us-central1-docker.pkg.dev/k8s-artifacts-prod/images"},
			},
			Provenance: policy,
			Filepath:   src + "/promoter-manifest.yaml",
		}
	}

	policies, err := ProvenancePolicies([]Manifest{
		manifest(testPolicySrc, requirePolicy),
		manifest("gcr.io/b", nil),
		manifest(testPolicySrc, requirePolicy),
		{},
	})
	require.NoError(t, err)
	require.Equal(t, map[image.Registry]*provenance.Policy{
		testPolicySrc: requirePolicy,
		"gcr.io/b":    nil,
	}, policies)

	// Off and no policy are the same.
	_, err = ProvenancePolicies([]Manifest{
		manifest("gcr.io/b", nil),
		manifest("gcr.io/b", &provenance.Policy{Mode: provenance.PolicyModeOff}),
	})
	require.NoError(t, err)

	for _, conflict := range [][]Manifest{
		{manifest(testPolicySrc, requirePolicy), manifest(testPolicySrc, warn)},
		{manifest(testPolicySrc, nil), manifest(testPolicySrc, warn)},
		{manifest(testPolicySrc, requirePolicy), manifest("GCR.io/a/", warn)},
	} {
		_, err = ProvenancePolicies(conflict)
		require.ErrorContains(t, err, "declare different provenance policies for source registry gcr.io/a")
	}
}

func TestApplicableProvenancePolicies(t *testing.T) {
	t.Parallel()

	parent := &provenance.Policy{Mode: provenance.PolicyModeWarn}
	nested := &provenance.Policy{Mode: provenance.PolicyModeRequire}

	policies, err := ProvenancePolicies([]Manifest{
		{Registries: []registry.Context{{Name: "GCR.io/a/", Src: true}}, Provenance: parent},
		{Registries: []registry.Context{{Name: testNestedSrc, Src: true}}, Provenance: nested},
		// A nested manifest without a policy does not switch the parent's
		// policy off.
		{Registries: []registry.Context{{Name: "gcr.io/a/open", Src: true}}},
	})
	require.NoError(t, err)

	// The image names are relative to the source registry of each policy.
	inParent := func(img string) AppliedProvenancePolicy { return AppliedProvenancePolicy{Policy: parent, Image: img} }
	inNested := AppliedProvenancePolicy{Policy: nested, Image: testPolicyImage}

	for _, tc := range []struct {
		registry image.Registry
		name     image.Name
		want     []AppliedProvenancePolicy
	}{
		{registry: testPolicySrc, name: testPolicyImage, want: []AppliedProvenancePolicy{inParent(testPolicyImage)}},
		{registry: "gcr.io/a/", name: testPolicyImage, want: []AppliedProvenancePolicy{inParent(testPolicyImage)}},
		{registry: "Gcr.io/a", name: testPolicyImage, want: []AppliedProvenancePolicy{inParent(testPolicyImage)}},
		{registry: testNestedSrc, name: testPolicyImage, want: []AppliedProvenancePolicy{inParent("nested/img"), inNested}},
		{registry: testPolicySrc, name: "nested/img", want: []AppliedProvenancePolicy{inParent("nested/img"), inNested}},
		{registry: "gcr.io/a/open", name: testPolicyImage, want: []AppliedProvenancePolicy{inParent("open/img")}},
		{registry: testPolicySrc, name: "nested-img", want: []AppliedProvenancePolicy{inParent("nested-img")}},
		{registry: "gcr.io/ab", name: testPolicyImage},
		{registry: "gcr.io", name: "a/img", want: []AppliedProvenancePolicy{inParent(testPolicyImage)}},
		{registry: "gcr.io/other", name: testPolicyImage},
	} {
		require.Equal(t, tc.want, ApplicableProvenancePolicies(policies, tc.registry, tc.name),
			"%s %s", tc.registry, tc.name)
	}
}

func TestApplicableProvenancePoliciesImages(t *testing.T) {
	t.Parallel()

	// Like k8s-staging-kubernetes: the release images have a policy, pause
	// is promoted as without one.
	scoped := &provenance.Policy{Mode: provenance.PolicyModeWarn, Images: []string{testScopePattern, "conformance*"}}
	nested := &provenance.Policy{Mode: provenance.PolicyModeRequire}

	policies, err := ProvenancePolicies([]Manifest{
		{Registries: []registry.Context{{Name: testPolicySrc, Src: true}}, Provenance: scoped},
		{Registries: []registry.Context{{Name: testNestedSrc, Src: true}}, Provenance: nested},
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		registry image.Registry
		name     image.Name
		want     []AppliedProvenancePolicy
	}{
		{registry: testPolicySrc, name: testScopeImage, want: []AppliedProvenancePolicy{{Policy: scoped, Image: testScopeImage}}},
		{registry: testPolicySrc, name: "conformance-amd64", want: []AppliedProvenancePolicy{{Policy: scoped, Image: "conformance-amd64"}}},
		{registry: testPolicySrc, name: "pause"},
		// The images of a nested registry are named with their path, which
		// the scope of the parent doesn't match, so only the nested policy
		// applies.
		{registry: testNestedSrc, name: testScopeImage, want: []AppliedProvenancePolicy{{Policy: nested, Image: testScopeImage}}},
	} {
		require.Equal(t, tc.want, ApplicableProvenancePolicies(policies, tc.registry, tc.name),
			"%s %s", tc.registry, tc.name)
	}

	// The policy of the parent leaves pause out, the nested one has no
	// images.
	require.Equal(t, []image.Registry{testPolicySrc}, ExcludedProvenancePolicies(policies, testPolicySrc, "pause"))
	require.Empty(t, ExcludedProvenancePolicies(policies, testPolicySrc, testScopeImage))
	require.Empty(t, ExcludedProvenancePolicies(policies, "gcr.io/other", "pause"))

	// The parent policy leaves out an image of the nested registry, which
	// the nested policy covers, so the image isn't without a policy.
	require.Equal(t, []image.Registry{testPolicySrc}, ExcludedProvenancePolicies(policies, testNestedSrc, testScopeImage))
	require.Equal(t, []AppliedProvenancePolicy{{Policy: nested, Image: testScopeImage}},
		ApplicableProvenancePolicies(policies, testNestedSrc, testScopeImage))

	// The image at a nested source registry, which a parent manifest
	// names with the path of the nested registry, stays in the scope of the
	// nested policy, which names it as its own.
	scopedNested := &provenance.Policy{Mode: provenance.PolicyModeRequire, Images: []string{testScopePattern}}
	policies, err = ProvenancePolicies([]Manifest{
		{Registries: []registry.Context{{Name: testNestedSrc, Src: true}}, Provenance: scopedNested},
	})
	require.NoError(t, err)
	require.Equal(t, []AppliedProvenancePolicy{{Policy: scopedNested}},
		ApplicableProvenancePolicies(policies, testPolicySrc, "nested"))
	require.Empty(t, ExcludedProvenancePolicies(policies, testPolicySrc, "nested"))

	// Manifests of the same source registry that differ only in the images
	// conflict.
	_, err = ProvenancePolicies([]Manifest{
		{Registries: []registry.Context{{Name: testPolicySrc, Src: true}}, Provenance: scoped},
		{Registries: []registry.Context{{Name: testPolicySrc, Src: true}}, Provenance: &provenance.Policy{Mode: provenance.PolicyModeWarn}},
	})
	require.ErrorContains(t, err, "declare different provenance policies")
}

func TestUnmatchedPatterns(t *testing.T) {
	t.Parallel()

	withLevels := func(mode provenance.PolicyMode) *provenance.Policy {
		return &provenance.Policy{
			Mode:   mode,
			Levels: []provenance.ImageLevel{{Images: []string{testPolicyImage, "nested/*", testTypoPattern}, Level: 3}},
		}
	}

	manifest := func(src string, policy *provenance.Policy, images ...image.Name) Manifest {
		mfest := Manifest{
			Registries: []registry.Context{{Name: image.Registry(src), Src: true}},
			Provenance: policy,
			Filepath:   src + "/promoter-manifest.yaml",
		}

		for _, name := range images {
			mfest.Images = append(mfest.Images, registry.Image{Name: name})
		}

		return mfest
	}

	// The images of a nested manifest count for the parent policy, those
	// of a sibling registry don't.
	require.Equal(t, map[string]provenance.UnmatchedPatterns{
		testPolicySrc + "/promoter-manifest.yaml": {Levels: []string{testTypoPattern}},
	},
		unmatchedPatterns([]Manifest{
			manifest(testPolicySrc, withLevels(provenance.PolicyModeWarn), testPolicyImage),
			manifest(testNestedSrc, nil, "chart"),
			manifest("gcr.io/ab", nil, testTypoPattern),
		}))

	require.Empty(t, unmatchedPatterns([]Manifest{
		manifest(testPolicySrc, withLevels(provenance.PolicyModeWarn), testPolicyImage, "nested/chart", testTypoPattern),
	}), "every pattern matches")

	require.Empty(t, unmatchedPatterns([]Manifest{
		manifest(testPolicySrc, withLevels(provenance.PolicyModeOff)),
		manifest("gcr.io/b", nil, testPolicyImage),
	}), "a policy that is off requires nothing")

	// The images patterns too, and a levels pattern that matches only
	// images the policy doesn't apply to is reported on its own.
	scoped := withLevels(provenance.PolicyModeWarn)
	scoped.Images = []string{testPolicyImage, "nested/*", testScopePattern}

	require.Equal(t, map[string]provenance.UnmatchedPatterns{
		testPolicySrc + "/promoter-manifest.yaml": {Images: []string{testScopePattern}, Excluded: []string{testTypoPattern}},
	},
		unmatchedPatterns([]Manifest{
			manifest(testPolicySrc, scoped, testPolicyImage, "nested/chart", testTypoPattern),
		}))
}
