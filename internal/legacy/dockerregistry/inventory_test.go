/*
Copyright 2019 The Kubernetes Authors.

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

package inventory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	cr "github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry/registry"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry/schema"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/json"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/stream"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

type ParseJSONStreamResult struct {
	jsons json.Objects
	err   error
}

func TestReadJSONStream(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedOutput ParseJSONStreamResult
	}{
		{
			name:  "Blank input stream",
			input: `[]`,
			expectedOutput: ParseJSONStreamResult{
				json.Objects{},
				nil,
			},
		},
		// The order of the maps matters.
		{
			name: "Simple case",
			input: `[
  {
    "name": "gcr.io/louhi-gke-k8s/addon-resizer"
  },
  {
    "name": "gcr.io/louhi-gke-k8s/pause"
  }
]`,
			expectedOutput: ParseJSONStreamResult{
				json.Objects{
					{"name": "gcr.io/louhi-gke-k8s/addon-resizer"},
					{"name": "gcr.io/louhi-gke-k8s/pause"},
				},
				nil,
			},
		},
		// The order of the maps matters.
		{
			"Expected failure: missing closing brace",
			`[
  {
    "name": "gcr.io/louhi-gke-k8s/addon-resizer"
  ,
]`,
			ParseJSONStreamResult{
				nil,
				errors.New("yaml: line 4: did not find expected node content"),
			},
		},
	}

	// Test only the JSON unmarshalling logic.
	for _, test := range tests {
		var sr stream.Fake
		sr.Bytes = []byte(test.input)
		stdout, _, err := sr.Produce()

		// The fake should never error out when producing a stdout stream for
		// us.
		require.NoError(t, err)

		jsons, err := json.Consume(stdout)
		_ = sr.Close()

		// Check the error as well (at the very least, we can check that the
		// error was nil).
		require.Equal(t, test.expectedOutput.err, err)

		got := jsons
		expected := test.expectedOutput.jsons
		require.Equal(t, expected, got)
	}
}

func TestParseRegistryManifest(t *testing.T) {
	// TODO: Create a function to convert an Manifest to a YAML
	// representation, and vice-versa.
	//
	// TODO: Use property-based testing to test the fidelity of the conversion
	// (marshaling/unmarshaling) functions.
	tests := []struct {
		name           string
		input          string
		expectedOutput schema.Manifest
		expectedError  error
	}{
		{
			"Empty manifest (invalid)",
			``,
			schema.Manifest{},
			errors.New(`'registries' field cannot be empty`),
		},
		{
			"Stub manifest (`images` field is empty)",
			`registries:
- name: gcr.io/bar
  service-account: foobar@google-containers.iam.gserviceaccount.com
- name: gcr.io/foo
  service-account: src@google-containers.iam.gserviceaccount.com
  src: true
images: []
`,
			schema.Manifest{
				Registries: []registry.Context{
					{
						Name:           "gcr.io/bar",
						ServiceAccount: "foobar@google-containers.iam.gserviceaccount.com",
					},
					{
						Name:           "gcr.io/foo",
						ServiceAccount: "src@google-containers.iam.gserviceaccount.com",
						Src:            true,
					},
				},

				Images: []registry.Image{},
			},
			nil,
		},
		{
			"Basic manifest",
			`registries:
- name: gcr.io/bar
  service-account: foobar@google-containers.iam.gserviceaccount.com
- name: gcr.io/foo
  service-account: src@google-containers.iam.gserviceaccount.com
  src: true
images:
- name: agave
  dmap:
    "sha256:aab34c5841987a1b133388fa9f27e7960c4b1307e2f9147dca407ba26af48a54": ["latest"]
- name: banana
  dmap:
    "sha256:07353f7b26327f0d933515a22b1de587b040d3d85c464ea299c1b9f242529326": [ "1.8.3" ]  # Branches: ['master']
`,
			schema.Manifest{
				Registries: []registry.Context{
					{
						Name:           "gcr.io/bar",
						ServiceAccount: "foobar@google-containers.iam.gserviceaccount.com",
					},
					{
						Name:           "gcr.io/foo",
						ServiceAccount: "src@google-containers.iam.gserviceaccount.com",
						Src:            true,
					},
				},

				Images: []registry.Image{
					{
						Name: "agave",
						Dmap: registry.DigestTags{
							"sha256:aab34c5841987a1b133388fa9f27e7960c4b1307e2f9147dca407ba26af48a54": {"latest"},
						},
					},
					{
						Name: "banana",
						Dmap: registry.DigestTags{
							"sha256:07353f7b26327f0d933515a22b1de587b040d3d85c464ea299c1b9f242529326": {"1.8.3"},
						},
					},
				},
			},
			nil,
		},
		{
			"Missing src registry in registries (invalid)",
			`registries:
- name: gcr.io/bar
  service-account: foobar@google-containers.iam.gserviceaccount.com
- name: gcr.io/foo
  service-account: src@google-containers.iam.gserviceaccount.com
images:
- name: agave
  dmap:
    "sha256:aab34c5841987a1b133388fa9f27e7960c4b1307e2f9147dca407ba26af48a54": ["latest"]
- name: banana
  dmap:
    "sha256:07353f7b26327f0d933515a22b1de587b040d3d85c464ea299c1b9f242529326": [ "1.8.3" ]  # Branches: ['master']
`,
			schema.Manifest{},
			errors.New("source registry must be set"),
		},
	}

	// Test only the JSON unmarshalling logic.
	for _, test := range tests {
		b := []byte(test.input)
		imageManifest, err := schema.ParseManifestYAML(b)

		// Check the error as well (at the very least, we can check that the
		// error was nil).
		require.Equal(t, test.expectedError, err)

		// There is nothing more to check if we expected a parse failure.
		if test.expectedError != nil {
			continue
		}

		got := imageManifest
		expected := test.expectedOutput
		require.Equal(t, expected, got)
	}
}

func TestParseThinManifestsFromDir(t *testing.T) {
	pwd := bazelTestPath("TestParseThinManifestsFromDir")

	tests := []struct {
		name string
		// "input" is folder name, relative to the location of this source file.
		input              string
		expectedOutput     []schema.Manifest
		expectedParseError error
	}{
		{
			"No manifests found (invalid)",
			"empty",
			[]schema.Manifest{},
			&os.PathError{
				Op:   "stat",
				Path: filepath.Join(pwd, "empty", "images"),
				Err:  errors.New("no such file or directory"),
			},
		},
		{
			"Singleton (single manifest)",
			"singleton",
			[]schema.Manifest{
				{
					Registries: []registry.Context{
						{
							Name:           "gcr.io/foo-staging",
							ServiceAccount: "sa@robot.com",
							Src:            true,
						},
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
					Filepath: "manifests/a/promoter-manifest.yaml",
				},
			},
			nil,
		},
		{
			"Multiple (with 'rebase')",
			"multiple-rebases",
			[]schema.Manifest{
				{
					Registries: []registry.Context{
						{
							Name:           "gcr.io/foo-staging",
							ServiceAccount: "sa@robot.com",
							Src:            true,
						},
						{
							Name:           "us.gcr.io/some-prod/foo",
							ServiceAccount: "sa@robot.com",
						},
						{
							Name:           "eu.gcr.io/some-prod/foo",
							ServiceAccount: "sa@robot.com",
						},
						{
							Name:           "asia.gcr.io/some-prod/foo",
							ServiceAccount: "sa@robot.com",
						},
					},
					Images: []registry.Image{
						{
							Name: "foo-controller",
							Dmap: registry.DigestTags{
								"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {"1.0"},
							},
						},
					},
					Filepath: "manifests/a/promoter-manifest.yaml",
				},
				{
					Registries: []registry.Context{
						{
							Name:           "gcr.io/bar-staging",
							ServiceAccount: "sa@robot.com",
							Src:            true,
						},
						{
							Name:           "us.gcr.io/some-prod/bar",
							ServiceAccount: "sa@robot.com",
						},
						{
							Name:           "eu.gcr.io/some-prod/bar",
							ServiceAccount: "sa@robot.com",
						},
						{
							Name:           "asia.gcr.io/some-prod/bar",
							ServiceAccount: "sa@robot.com",
						},
					},
					Images: []registry.Image{
						{
							Name: "bar-controller",
							Dmap: registry.DigestTags{
								"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": {"1.0"},
							},
						},
					},
					Filepath: "manifests/b/promoter-manifest.yaml",
				},
			},
			nil,
		},
		{
			"Basic (multiple thin manifests)",
			"basic-thin",
			[]schema.Manifest{
				{
					Registries: []registry.Context{
						{
							Name:           "gcr.io/foo-staging",
							ServiceAccount: "sa@robot.com",
							Src:            true,
						},
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
								"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {"1.0"},
							},
						},
					},
					Filepath: "manifests/a/promoter-manifest.yaml",
				},
				{
					Registries: []registry.Context{
						{
							Name:           "gcr.io/bar-staging",
							ServiceAccount: "sa@robot.com",
							Src:            true,
						},
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
							Name: "bar-controller",
							Dmap: registry.DigestTags{
								"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": {"1.0"},
							},
						},
					},
					Filepath: "manifests/b/promoter-manifest.yaml",
				},
				{
					Registries: []registry.Context{
						{
							Name:           "gcr.io/cat-staging",
							ServiceAccount: "sa@robot.com",
							Src:            true,
						},
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
							Name: "cat-controller",
							Dmap: registry.DigestTags{
								"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc": {"1.0"},
							},
						},
					},
					Filepath: "manifests/c/promoter-manifest.yaml",
				},
				{
					Registries: []registry.Context{
						{
							Name:           "gcr.io/qux-staging",
							ServiceAccount: "sa@robot.com",
							Src:            true,
						},
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
							Name: "qux-controller",
							Dmap: registry.DigestTags{
								"sha256:0000000000000000000000000000000000000000000000000000000000000000": {"1.0"},
							},
						},
					},
					Filepath: "manifests/d/promoter-manifest.yaml",
				},
			},
			nil,
		},
	}

	for _, test := range tests {
		fixtureDir := bazelTestPath("TestParseThinManifestsFromDir", test.input)

		// Fixup expected filepaths to match bazel's testing directory.
		expectedModified := test.expectedOutput[:0]
		for _, mfest := range test.expectedOutput {
			mfest.Filepath = filepath.Join(fixtureDir, mfest.Filepath)
			expectedModified = append(expectedModified, mfest)
		}

		got, errParse := schema.ParseThinManifestsFromDir(fixtureDir, false)

		// Clear private fields (redundant data) that are calculated on-the-fly
		// (it's too verbose to include them here; besides, it's not what we're
		// testing).
		gotModified := got[:0]
		for _, mfest := range got {
			mfest.SrcRegistry = nil
			gotModified = append(gotModified, mfest)
		}

		// Check the error as well (at the very least, we can check that the
		// error was nil).
		var errParseStr string
		var expectedParseErrorStr string
		if errParse != nil {
			errParseStr = errParse.Error()
		}
		if test.expectedParseError != nil {
			expectedParseErrorStr = test.expectedParseError.Error()
		}
		require.Equal(t, expectedParseErrorStr, errParseStr)

		// There is nothing more to check if we expected a parse failure.
		if test.expectedParseError != nil {
			continue
		}

		require.Equal(t, test.expectedOutput, gotModified)
	}
}

func TestValidateThinManifestsFromDir(t *testing.T) {
	shouldBeValid := []string{
		"singleton",
		"multiple-rebases",
		"overlapping-src-registries",
		"overlapping-destination-vertices-same-digest",
		"malformed-directory-tree-structure-bad-prefix-is-ignored",
	}

	pwd := bazelTestPath("TestValidateThinManifestsFromDir")

	for _, testInput := range shouldBeValid {
		fixtureDir := filepath.Join(pwd, "valid", testInput)

		mfests, errParse := schema.ParseThinManifestsFromDir(fixtureDir, false)
		require.NoError(t, errParse)

		_, edgeErr := ToPromotionEdges(mfests)
		require.NoError(t, edgeErr)
	}

	shouldBeInvalid := []struct {
		dirName            string
		expectedParseError error
		expectedEdgeError  error
	}{
		{
			"empty",
			&os.PathError{
				Op:   "stat",
				Path: filepath.Join(pwd, "invalid", "empty", "images"),
				Err:  errors.New("no such file or directory"),
			},
			nil,
		},
		{
			"overlapping-destination-vertices-different-digest",
			nil,
			errors.New("overlapping edges detected"),
		},
		{
			"malformed-directory-tree-structure",
			fmt.Errorf(
				"corresponding file %q does not exist",
				filepath.Join(pwd, "invalid", "malformed-directory-tree-structure", "images", "b", "images.yaml"),
			),
			nil,
		},
		{
			"malformed-directory-tree-structure-nested",
			fmt.Errorf(
				"unexpected manifest path %q",
				filepath.Join(pwd, "invalid", "malformed-directory-tree-structure-nested", "manifests", "b", "c", "promoter-manifest.yaml"),
			),
			nil,
		},
	}

	for _, test := range shouldBeInvalid {
		fixtureDir := bazelTestPath("TestValidateThinManifestsFromDir", "invalid", test.dirName)

		// It could be that a manifest, taken individually, failed on its own,
		// before we even get to ValidateThinManifestsFromDir(). So handle these
		// cases as well.
		mfests, errParse := schema.ParseThinManifestsFromDir(fixtureDir, false)

		var errParseStr string
		var expectedParseErrorStr string
		if errParse != nil {
			errParseStr = errParse.Error()
		}

		if test.expectedParseError != nil {
			expectedParseErrorStr = test.expectedParseError.Error()
		}

		require.Equal(t, expectedParseErrorStr, errParseStr)

		_, edgeErr := ToPromotionEdges(mfests)
		require.Equal(t, test.expectedEdgeError, edgeErr)
	}
}

func TestParseImageDigest(t *testing.T) {
	shouldBeValid := []string{
		`sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`,
		`sha256:0000000000000000000000000000000000000000000000000000000000000000`,
		`sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff`,
		`sha256:3243f6a8885a308d313198a2e03707344a4093822299f31d0082efa98ec4e6c8`,
	}

	for _, testInput := range shouldBeValid {
		d := image.Digest(testInput)
		got := schema.ValidateDigest(d)
		require.NoError(t, got)
	}

	shouldBeInvalid := []string{
		// Empty.
		``,
		// Too short.
		`sha256:0`,
		// Too long.
		`sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef1`,
		// Invalid character 'x'.
		`sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdex`,
		// No prefix 'sha256'.
		`0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`,
	}

	for _, testInput := range shouldBeInvalid {
		d := image.Digest(testInput)
		got := schema.ValidateDigest(d)
		require.Equal(t, fmt.Errorf("invalid digest: %v", d), got)
	}
}

func TestParseImageTag(t *testing.T) {
	shouldBeValid := []string{
		`a`,
		`_`,
		`latest`,
		`_latest`,
		// Awkward, but valid.
		`_____----hello........`,
		// Longest tag is 128 chars.
		`this-is-exactly-128-chars-this-is-exactly-128-chars-this-is-exactly-128-chars-this-is-exactly-128-chars-this-is-exactly-128-char`,
	}

	for _, testInput := range shouldBeValid {
		tag := image.Tag(testInput)
		got := schema.ValidateTag(tag)
		require.NoError(t, got)
	}

	shouldBeInvalid := []string{
		// Empty.
		``,
		// Does not begin with an ASCII word character.
		`.`,
		// Does not begin with an ASCII word character.
		`-`,
		// Unicode not allowed.
		`안녕`,
		// No spaces allowed.
		`a b`,
		// Too long (>128 ASCII chars).
		`this-is-longer-than-128-chars-this-is-longer-than-128-chars-this-is-longer-than-128-chars-this-is-longer-than-128-chars-this-is-l`,
	}

	for _, testInput := range shouldBeInvalid {
		tag := image.Tag(testInput)
		got := schema.ValidateTag(tag)
		require.Equal(t, fmt.Errorf("invalid tag: %v", tag), got)
	}
}

func TestValidateRegistryImagePath(t *testing.T) {
	shouldBeValid := []string{
		`gcr.io/foo/bar`,
		`k8s.gcr.io/foo`,
		`staging-k8s.gcr.io/foo`,
		`staging-k8s.gcr.io/foo/bar/nested/path/image`,
	}

	for _, testInput := range shouldBeValid {
		rip := RegistryImagePath(testInput)
		got := ValidateRegistryImagePath(rip)
		require.NoError(t, got)
	}

	shouldBeInvalid := []string{
		// Empty.
		``,
		// No dot.
		`gcrio`,
		// Too many dots.
		`gcr..io`,
		// Leading dot.
		`.gcr.io`,
		// Trailing dot.
		`gcr.io.`,
		// Too many slashes.
		`gcr.io//foo`,
		// Leading slash.
		`/gcr.io`,
		// Trailing slash (1).
		`gcr.io/`,
		// Trailing slash (2).
		`gcr.io/foo/`,
	}

	for _, testInput := range shouldBeInvalid {
		rip := RegistryImagePath(testInput)
		got := ValidateRegistryImagePath(rip)
		require.Equal(
			t, fmt.Errorf("invalid registry image path: %v", rip), got,
		)
	}
}

func TestSplitRegistryImagePath(t *testing.T) {
	knownRegistryNames := []image.Registry{
		`gcr.io/foo`,
		`us.gcr.io/foo`,
		`k8s.gcr.io`,
		`eu.gcr.io/foo/d`,
	}

	tests := []struct {
		name                 string
		input                RegistryImagePath
		expectedRegistryName image.Registry
		expectedImageName    image.Name
		expectedErr          error
	}{
		{
			`basic gcr.io`,
			`gcr.io/foo/a/b/c`,
			`gcr.io/foo`,
			`a/b/c`,
			nil,
		},
		{
			`regional GCR`,
			`us.gcr.io/foo/a/b/c`,
			`us.gcr.io/foo`,
			`a/b/c`,
			nil,
		},
		{
			`regional GCR (extra level of nesting)`,
			`eu.gcr.io/foo/d/e/f`,
			`eu.gcr.io/foo/d`,
			`e/f`,
			nil,
		},
		{
			`vanity GCR`,
			`k8s.gcr.io/a/b/c`,
			`k8s.gcr.io`,
			`a/b/c`,
			nil,
		},
	}
	for _, test := range tests {
		rName, iName, err := SplitRegistryImagePath(test.input, knownRegistryNames)
		require.Equal(t, test.expectedRegistryName, rName)
		require.Equal(t, test.expectedImageName, iName)
		require.Equal(t, test.expectedErr, err)
	}
}

func TestSplitByKnownRegistries(t *testing.T) {
	knownRegistryNames := []image.Registry{
		// See
		// https://github.com/kubernetes-sigs/promo-tools/issues/188.
		`us.gcr.io/k8s-artifacts-prod/kube-state-metrics`,
		`us.gcr.io/k8s-artifacts-prod/metrics-server`,
		`us.gcr.io/k8s-artifacts-prod`,
	}
	knownRegistryContexts := make([]registry.Context, 0)
	for _, knownRegistryName := range knownRegistryNames {
		rc := registry.Context{}
		rc.Name = knownRegistryName
		knownRegistryContexts = append(knownRegistryContexts, rc)
	}

	tests := []struct {
		name                 string
		input                image.Registry
		expectedRegistryName image.Registry
		expectedImageName    image.Name
		expectedErr          error
	}{
		{
			`image at toplevel root path`,
			`us.gcr.io/k8s-artifacts-prod/kube-state-metrics`,
			`us.gcr.io/k8s-artifacts-prod`,
			`kube-state-metrics`,
			nil,
		},
		{
			`unclean split (known repo cuts into middle of image name)`,
			`us.gcr.io/k8s-artifacts-prod/metrics-server-amd64`,
			`us.gcr.io/k8s-artifacts-prod`,
			`metrics-server-amd64`,
			nil,
		},
	}
	for _, test := range tests {
		rootReg, imageName, err := SplitByKnownRegistries(test.input, knownRegistryContexts)
		require.Equal(t, test.expectedRegistryName, rootReg)
		require.Equal(t, test.expectedImageName, imageName)
		require.Equal(t, test.expectedErr, err)
	}
}

func TestCommandGeneration(t *testing.T) {
	destRC := registry.Context{
		Name:           "gcr.io/foo",
		ServiceAccount: "robot",
	}

	var (
		destImageName image.Name   = "baz"
		digest        image.Digest = "sha256:000"
	)

	got := GetDeleteCmd(
		destRC,
		true,
		destImageName,
		digest,
		false)

	expected := []string{
		"gcloud",
		"--account=robot",
		"container",
		"images",
		"delete",
		ToFQIN(destRC.Name, destImageName, digest),
		"--format=json",
	}

	require.Equal(t, expected, got)

	got = GetDeleteCmd(
		destRC,
		false,
		destImageName,
		digest,
		false,
	)

	expected = []string{
		"gcloud",
		"container",
		"images",
		"delete",
		ToFQIN(destRC.Name, destImageName, digest),
		"--format=json",
	}

	require.Equal(t, expected, got)
}

// TestReadRegistries tests reading images and tags from a registry.
func TestReadRegistries(t *testing.T) {
	const fakeRegName image.Registry = "gcr.io/foo"

	tests := []struct {
		name           string
		input          map[string]string
		expectedOutput registry.RegInvImage
	}{
		{
			"Only toplevel repos (no child repos)",
			map[string]string{
				"gcr.io/foo": `{
  "child": [
    "addon-resizer",
    "pause"
  ],
  "manifest": {},
  "name": "foo",
  "tags": []
}`,
				"gcr.io/foo/addon-resizer": `{
  "child": [],
  "manifest": {
    "sha256:b5b2d91319f049143806baeacc886f82f621e9a2550df856b11b5c22db4570a7": {
      "imageSizeBytes": "12875324",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.schema.v2+json",
      "tag": [
        "latest"
      ],
      "timeCreatedMs": "1501774217070",
      "timeUploadedMs": "1552917295327"
    },
    "sha256:0519a83e8f217e33dd06fe7a7347444cfda5e2e29cf52aaa24755999cb104a4d": {
      "imageSizeBytes": "12875324",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.schema.v2+json",
      "tag": [
        "1.0"
      ],
      "timeCreatedMs": "1501774217070",
      "timeUploadedMs": "1552917295327"
    }
  },
  "name": "foo/addon-resizer",
  "tags": [
    "latest",
    "1.0"
  ]
}`,
				"gcr.io/foo/pause": `{
  "child": [],
  "manifest": {
    "sha256:06fdf10aae2eeeac5a82c213e4693f82ab05b3b09b820fce95a7cac0bbdad534": {
      "imageSizeBytes": "12875324",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.schema.v2+json",
      "tag": [
        "v1.2.3"
      ],
      "timeCreatedMs": "1501774217070",
      "timeUploadedMs": "1552917295327"
    }
  },
  "name": "foo/pause",
  "tags": [
    "v1.2.3"
  ]
}`,
			},
			registry.RegInvImage{
				"addon-resizer": {
					"sha256:b5b2d91319f049143806baeacc886f82f621e9a2550df856b11b5c22db4570a7": {"latest"},
					"sha256:0519a83e8f217e33dd06fe7a7347444cfda5e2e29cf52aaa24755999cb104a4d": {"1.0"},
				},
				"pause": {
					"sha256:06fdf10aae2eeeac5a82c213e4693f82ab05b3b09b820fce95a7cac0bbdad534": {"v1.2.3"},
				},
			},
		},
		{
			"Recursive repos (child repos)",
			map[string]string{
				"gcr.io/foo": `{
  "child": [
    "addon-resizer",
    "pause"
  ],
  "manifest": {},
  "name": "foo",
  "tags": []
}`,
				"gcr.io/foo/addon-resizer": `{
  "child": [],
  "manifest": {
    "sha256:b5b2d91319f049143806baeacc886f82f621e9a2550df856b11b5c22db4570a7": {
      "imageSizeBytes": "12875324",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.schema.v2+json",
      "tag": [
        "latest"
      ],
      "timeCreatedMs": "1501774217070",
      "timeUploadedMs": "1552917295327"
    },
    "sha256:0519a83e8f217e33dd06fe7a7347444cfda5e2e29cf52aaa24755999cb104a4d": {
      "imageSizeBytes": "12875324",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.schema.v2+json",
      "tag": [
        "1.0"
      ],
      "timeCreatedMs": "1501774217070",
      "timeUploadedMs": "1552917295327"
    }
  },
  "name": "foo/addon-resizer",
  "tags": [
    "latest",
    "1.0"
  ]
}`,
				"gcr.io/foo/pause": `{
  "child": [
    "childLevel1"
  ],
  "manifest": {
    "sha256:06fdf10aae2eeeac5a82c213e4693f82ab05b3b09b820fce95a7cac0bbdad534": {
      "imageSizeBytes": "12875324",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.schema.v2+json",
      "tag": [
        "v1.2.3"
      ],
      "timeCreatedMs": "1501774217070",
      "timeUploadedMs": "1552917295327"
    }
  },
  "name": "foo/pause",
  "tags": [
    "v1.2.3"
  ]
}`,
				"gcr.io/foo/pause/childLevel1": `{
  "child": [
    "childLevel2"
  ],
  "manifest": {
    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {
      "imageSizeBytes": "12875324",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.schema.v2+json",
      "tag": [
        "aaa"
      ],
      "timeCreatedMs": "1501774217070",
      "timeUploadedMs": "1552917295327"
    }
  },
  "name": "foo/pause/childLevel1",
  "tags": [
    "aaa"
  ]
}`,
				"gcr.io/foo/pause/childLevel1/childLevel2": `{
  "child": [],
  "manifest": {
    "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff": {
      "imageSizeBytes": "12875324",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.schema.v2+json",
      "tag": [
        "fff"
      ],
      "timeCreatedMs": "1501774217070",
      "timeUploadedMs": "1552917295327"
    }
  },
  "name": "foo/pause/childLevel1/childLevel2",
  "tags": [
    "fff"
  ]
}`,
			},
			registry.RegInvImage{
				"addon-resizer": {
					"sha256:b5b2d91319f049143806baeacc886f82f621e9a2550df856b11b5c22db4570a7": {"latest"},
					"sha256:0519a83e8f217e33dd06fe7a7347444cfda5e2e29cf52aaa24755999cb104a4d": {"1.0"},
				},
				"pause": {
					"sha256:06fdf10aae2eeeac5a82c213e4693f82ab05b3b09b820fce95a7cac0bbdad534": {"v1.2.3"},
				},
				"pause/childLevel1": {
					"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {"aaa"},
				},
				"pause/childLevel1/childLevel2": registry.DigestTags{
					"sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff": {"fff"},
				},
			},
		},
	}
	for _, test := range tests {
		// Destination registry is a placeholder, because ReadImageNames acts on
		// 2 registries (src and dest) at once.
		rcs := []registry.Context{
			{
				Name:           fakeRegName,
				ServiceAccount: "robot",
			},
		}

		sc := SyncContext{
			RegistryContexts: rcs,
			Inv:              map[image.Registry]registry.RegInvImage{fakeRegName: nil},
			DigestMediaType:  make(DigestMediaType),
		}

		mkFakeStream1 := func(_ *SyncContext, rc registry.Context) stream.Producer {
			var sr stream.Fake

			_, domain, repoPath := GetTokenKeyDomainRepoPath(rc.Name)
			fakeHTTPBody, ok := test.input[domain+"/"+repoPath]
			require.True(t, ok)

			sr.Bytes = []byte(fakeHTTPBody)
			return &sr
		}

		sc.ReadRegistries(rcs, true, mkFakeStream1)
		got := sc.Inv[fakeRegName]
		expected := test.expectedOutput
		require.Equal(t, expected, got)
	}
}

// TestReadGManifestLists tests reading ManifestList information from GCR.
func TestReadGManifestLists(t *testing.T) {
	const fakeRegName image.Registry = "gcr.io/foo"

	tests := []struct {
		name           string
		input          map[string]string
		expectedOutput ParentDigest
	}{
		{
			"Basic example",
			map[string]string{
				"gcr.io/foo/someImage": `{
   "schemaVersion": 2,
   "mediaType": "application/vnd.docker.distribution.schema.list.v2+json",
   "manifests": [
      {
         "mediaType": "application/vnd.docker.distribution.schema.v2+json",
         "size": 739,
         "digest": "sha256:0bd88bcba94f800715fca33ffc4bde430646a7c797237313cbccdcdef9f80f2d",
         "platform": {
            "architecture": "amd64",
            "os": "linux"
         }
      },
      {
         "mediaType": "application/vnd.docker.distribution.schema.v2+json",
         "size": 739,
         "digest": "sha256:0ad4f92011b2fa5de88a6e6a2d8b97f38371246021c974760e5fc54b9b7069e5",
         "platform": {
            "architecture": "s390x",
            "os": "linux"
         }
      }
   ]
}`,
			},
			ParentDigest{
				"sha256:0bd88bcba94f800715fca33ffc4bde430646a7c797237313cbccdcdef9f80f2d": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
				"sha256:0ad4f92011b2fa5de88a6e6a2d8b97f38371246021c974760e5fc54b9b7069e5": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
			},
		},
	}

	for _, test := range tests {
		// Destination registry is a placeholder, because ReadImageNames acts on
		// 2 registries (src and dest) at once.
		rcs := []registry.Context{
			{
				Name:           fakeRegName,
				ServiceAccount: "robot",
			},
		}
		sc := SyncContext{
			RegistryContexts: rcs,
			Inv: map[image.Registry]registry.RegInvImage{
				"gcr.io/foo": {
					"someImage": registry.DigestTags{
						"sha256:0000000000000000000000000000000000000000000000000000000000000000": {"1.0"},
					},
				},
			},
			DigestMediaType: DigestMediaType{
				"sha256:0000000000000000000000000000000000000000000000000000000000000000": cr.DockerManifestList,
			},
			ParentDigest: make(ParentDigest),
		}

		mkFakeStream1 := func(_ *SyncContext, gmlc *GCRManifestListContext) stream.Producer {
			var sr stream.Fake

			_, domain, repoPath := GetTokenKeyDomainRepoPath(gmlc.RegistryContext.Name)
			fakeHTTPBody, ok := test.input[domain+"/"+repoPath+"/"+string(gmlc.ImageName)]
			require.True(t, ok)

			sr.Bytes = []byte(fakeHTTPBody)
			return &sr
		}

		sc.ReadGCRManifestLists(mkFakeStream1)
		got := sc.ParentDigest
		expected := test.expectedOutput
		require.Equal(t, expected, got)
	}
}

func TestGetTokenKeyDomainRepoPath(t *testing.T) {
	type TokenKeyDomainRepoPath [3]string

	tests := []struct {
		name     string
		input    image.Registry
		expected TokenKeyDomainRepoPath
	}{
		{
			"basic",
			"gcr.io/foo/bar",
			[3]string{"gcr.io/foo", "gcr.io", "foo/bar"},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				tokenKey, domain, repoPath := GetTokenKeyDomainRepoPath(test.input)

				require.Equal(t, test.expected[0], tokenKey)

				require.Equal(t, test.expected[1], domain)

				require.Equal(t, test.expected[2], repoPath)
			},
		)
	}
}

func TestSetManipulationsRegistryInventories(t *testing.T) {
	tests := []struct {
		name           string
		input1         registry.RegInvImage
		input2         registry.RegInvImage
		op             func(a, b registry.RegInvImage) registry.RegInvImage
		expectedOutput registry.RegInvImage
	}{
		{
			"Set Minus",
			registry.RegInvImage{
				"foo": {
					"sha256:abc": {"1.0", "latest"},
				},
				"bar": {
					"sha256:def": {"0.9"},
				},
			},
			registry.RegInvImage{
				"foo": {
					"sha256:abc": {"1.0", "latest"},
				},
				"bar": {
					"sha256:def": {"0.9"},
				},
			},
			registry.RegInvImage.Minus,
			registry.RegInvImage{},
		},
		{
			"Set Union",
			registry.RegInvImage{
				"foo": {
					"sha256:abc": {"1.0", "latest"},
				},
				"bar": {
					"sha256:def": {"0.9"},
				},
			},
			registry.RegInvImage{
				"apple": {
					"sha256:abc": {"1.0", "latest"},
				},
				"banana": {
					"sha256:def": {"0.9"},
				},
			},
			registry.RegInvImage.Union,
			registry.RegInvImage{
				"foo": {
					"sha256:abc": {"1.0", "latest"},
				},
				"bar": {
					"sha256:def": {"0.9"},
				},
				"apple": {
					"sha256:abc": {"1.0", "latest"},
				},
				"banana": {
					"sha256:def": {"0.9"},
				},
			},
		},
	}

	for _, test := range tests {
		got := test.op(test.input1, test.input2)
		expected := test.expectedOutput
		require.Equal(t, expected, got)
	}
}

func TestSetManipulationsTags(t *testing.T) {
	tests := []struct {
		name           string
		input1         registry.TagSlice
		input2         registry.TagSlice
		op             func(a, b registry.TagSlice) registry.TagSet
		expectedOutput registry.TagSet
	}{
		{
			"Set Minus (both blank)",
			registry.TagSlice{},
			registry.TagSlice{},
			registry.TagSlice.Minus,
			registry.TagSet{},
		},
		{
			"Set Minus (first blank)",
			registry.TagSlice{},
			registry.TagSlice{"a"},
			registry.TagSlice.Minus,
			registry.TagSet{},
		},
		{
			"Set Minus (second blank)",
			registry.TagSlice{"a", "b"},
			registry.TagSlice{},
			registry.TagSlice.Minus,
			registry.TagSet{"a": nil, "b": nil},
		},
		{
			"Set Minus",
			registry.TagSlice{"a", "b"},
			registry.TagSlice{"b"},
			registry.TagSlice.Minus,
			registry.TagSet{"a": nil},
		},
		{
			"Set Union (both blank)",
			registry.TagSlice{},
			registry.TagSlice{},
			registry.TagSlice.Union,
			registry.TagSet{},
		},
		{
			"Set Union (first blank)",
			registry.TagSlice{},
			registry.TagSlice{"a"},
			registry.TagSlice.Union,
			registry.TagSet{"a": nil},
		},
		{
			"Set Union (second blank)",
			registry.TagSlice{"a"},
			registry.TagSlice{},
			registry.TagSlice.Union,
			registry.TagSet{"a": nil},
		},
		{
			"Set Union",
			registry.TagSlice{"a", "c"},
			registry.TagSlice{"b", "d"},
			registry.TagSlice.Union,
			registry.TagSet{"a": nil, "b": nil, "c": nil, "d": nil},
		},
		{
			"Set Intersection (no intersection)",
			registry.TagSlice{"a"},
			registry.TagSlice{"b"},
			registry.TagSlice.Intersection,
			registry.TagSet{},
		},
		{
			"Set Intersection (some intersection)",
			registry.TagSlice{"a", "b"},
			registry.TagSlice{"b", "c"},
			registry.TagSlice.Intersection,
			registry.TagSet{"b": nil},
		},
	}

	for _, test := range tests {
		got := test.op(test.input1, test.input2)
		expected := test.expectedOutput
		require.Equal(t, expected, got)
	}
}

func TestToPromotionEdges(t *testing.T) {
	srcRegName := image.Registry("gcr.io/foo")
	destRegName := image.Registry("gcr.io/bar")
	destRegName2 := image.Registry("gcr.io/cat")
	destRC := registry.Context{
		Name:           destRegName,
		ServiceAccount: "robot",
	}
	destRC2 := registry.Context{
		Name:           destRegName2,
		ServiceAccount: "robot",
	}
	srcRC := registry.Context{
		Name:           srcRegName,
		ServiceAccount: "robot",
		Src:            true,
	}
	registries1 := []registry.Context{destRC, srcRC}
	registries2 := []registry.Context{destRC, srcRC, destRC2}

	sc := SyncContext{
		Inv: MasterInventory{
			"gcr.io/foo": registry.RegInvImage{
				"a": {
					"sha256:000": {"0.9"},
				},
				"c": {
					"sha256:222": {"2.0"},
					"sha256:333": {"3.0"},
				},
			},
			"gcr.io/bar": {
				"a": {
					"sha256:000": {"0.9"},
				},
				"b": {
					"sha256:111": {},
				},
				"c": {
					"sha256:222": {"2.0"},
					"sha256:333": {"3.0"},
				},
			},
			"gcr.io/cat": {
				"a": {
					"sha256:000": {"0.9"},
				},
				"c": {
					"sha256:222": {"2.0"},
					"sha256:333": {"3.0"},
				},
			},
		},
	}

	tests := []struct {
		name                  string
		input                 []schema.Manifest
		expectedInitial       map[PromotionEdge]interface{}
		expectedInitialErr    error
		expectedFiltered      map[PromotionEdge]interface{}
		expectedFilteredClean bool
	}{
		{
			"Basic case (1 new edge; already promoted)",
			[]schema.Manifest{
				{
					Registries: registries1,
					Images: []registry.Image{
						{
							Name: "a",
							Dmap: registry.DigestTags{
								"sha256:000": {"0.9"},
							},
						},
					},
					SrcRegistry: &srcRC,
				},
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
				}: nil,
			},
			nil,
			make(map[PromotionEdge]interface{}),
			true,
		},
		{
			"Basic case (2 new edges; already promoted)",
			[]schema.Manifest{
				{
					Registries: registries2,
					Images: []registry.Image{
						{
							Name: "a",
							Dmap: registry.DigestTags{
								"sha256:000": {"0.9"},
							},
						},
					},
					SrcRegistry: &srcRC,
				},
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
				}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
					Digest:      "sha256:000",
					DstRegistry: destRC2,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
				}: nil,
			},
			nil,
			make(map[PromotionEdge]interface{}),
			true,
		},
		{
			"Tag move (tag swap image c:2.0 and c:3.0)",
			[]schema.Manifest{
				{
					Registries: registries2,
					Images: []registry.Image{
						{
							Name: "c",
							Dmap: registry.DigestTags{
								"sha256:222": {"3.0"},
								"sha256:333": {"2.0"},
							},
						},
					},
					SrcRegistry: &srcRC,
				},
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "c",
						Tag:  "2.0",
					},
					Digest:      "sha256:333",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "c",
						Tag:  "2.0",
					},
				}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "c",
						Tag:  "3.0",
					},
					Digest:      "sha256:222",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "c",
						Tag:  "3.0",
					},
				}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "c",
						Tag:  "2.0",
					},
					Digest:      "sha256:333",
					DstRegistry: destRC2,
					DstImageTag: ImageTag{
						Name: "c",
						Tag:  "2.0",
					},
				}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "c",
						Tag:  "3.0",
					},
					Digest:      "sha256:222",
					DstRegistry: destRC2,
					DstImageTag: ImageTag{
						Name: "c",
						Tag:  "3.0",
					},
				}: nil,
			},
			nil,
			make(map[PromotionEdge]interface{}),
			false,
		},
	}

	for _, test := range tests {
		// Finalize Manifests.
		for i := range test.input {
			// TODO(lint): Check error return value
			//nolint:errcheck
			_ = test.input[i].Finalize()
		}

		got, gotErr := ToPromotionEdges(test.input)
		require.Equal(t, test.expectedInitial, got)
		require.Equal(t, test.expectedInitialErr, gotErr)
		got, gotClean := sc.GetPromotionCandidates(got)
		require.Equal(t, test.expectedFiltered, got)
		require.Equal(t, test.expectedFilteredClean, gotClean)
	}
}

func TestCheckOverlappingEdges(t *testing.T) {
	srcRegName := image.Registry("gcr.io/foo")
	destRegName := image.Registry("gcr.io/bar")
	destRC := registry.Context{
		Name:           destRegName,
		ServiceAccount: "robot",
	}
	srcRC := registry.Context{
		Name:           srcRegName,
		ServiceAccount: "robot",
		Src:            true,
	}

	tests := []struct {
		name        string
		input       map[PromotionEdge]interface{}
		expected    map[PromotionEdge]interface{}
		expectedErr error
	}{
		{
			"Basic case (0 edges)",
			make(map[PromotionEdge]interface{}),
			make(map[PromotionEdge]interface{}),
			nil,
		},
		{
			"Basic case (singleton edge, no overlapping edges)",
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
				}: nil,
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
				}: nil,
			},
			nil,
		},
		{ //nolint:dupl
			"Basic case (two edges, no overlapping edges)",
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
				}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "b",
						Tag:  "0.9",
					},
					Digest:      "sha256:111",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "b",
						Tag:  "0.9",
					},
				}: nil,
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
				}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "b",
						Tag:  "0.9",
					},
					Digest:      "sha256:111",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "b",
						Tag:  "0.9",
					},
				}: nil,
			},
			nil,
		},
		{
			"Basic case (two edges, overlapped)",
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
				}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "b",
						Tag:  "0.9",
					},
					Digest:      "sha256:111",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
				}: nil,
			},
			nil,
			errors.New("overlapping edges detected"),
		},
		{ //nolint:dupl
			"Basic case (two tagless edges (different digests, same PQIN), no overlap)",
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "",
					},
				}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "b",
						Tag:  "0.9",
					},
					Digest:      "sha256:111",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "",
					},
				}: nil,
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "a",
						Tag:  "0.9",
					},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "",
					},
				}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						Name: "b",
						Tag:  "0.9",
					},
					Digest:      "sha256:111",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						Name: "a",
						Tag:  "",
					},
				}: nil,
			},
			nil,
		},
	}

	for _, test := range tests {
		got, gotErr := CheckOverlappingEdges(test.input)
		require.Equal(t, test.expected, got)
		require.Equal(t, test.expectedErr, gotErr)
	}
}

type FakeCheckAlwaysSucceed struct{}

func (c *FakeCheckAlwaysSucceed) Run() error {
	return nil
}

type FakeCheckAlwaysFail struct{}

func (c *FakeCheckAlwaysFail) Run() error {
	return errors.New("there was an error in the pull request check")
}

func TestRunChecks(t *testing.T) {
	sc := SyncContext{}

	tests := []struct {
		name     string
		checks   []PreCheck
		expected error
	}{
		{
			"Checking pull request with successful checks",
			[]PreCheck{
				&FakeCheckAlwaysSucceed{},
			},
			nil,
		},
		{
			"Checking pull request with unsuccessful checks",
			[]PreCheck{
				&FakeCheckAlwaysFail{},
			},
			errors.New("1 error(s) encountered during the prechecks"),
		},
		{
			"Checking pull request with successful and unsuccessful checks",
			[]PreCheck{
				&FakeCheckAlwaysSucceed{},
				&FakeCheckAlwaysFail{},
				&FakeCheckAlwaysFail{},
			},
			errors.New("2 error(s) encountered during the prechecks"),
		},
	}

	for _, test := range tests {
		got := sc.RunChecks(test.checks)
		require.Equal(t, test.expected, got)
	}
}

// TestPromotion is the most important test as it simulates the main job of the
// promoter.
func TestPromotion(t *testing.T) {
	// CapturedRequests is like a bitmap. We clear off bits (delete keys) for
	// each request that we see that got generated. Then it's just a matter of
	// ensuring that the map is empty. If it is not empty, we can just show what
	// it looks like (basically a list of all requests that did not get
	// generated).
	//
	// We could make it even more "powerful" by storing a histogram instead of a
	// set. Then we can check that all requests were generated exactly 1 time.
	srcRegName := image.Registry("gcr.io/foo")
	destRegName := image.Registry("gcr.io/bar")
	destRegName2 := image.Registry("gcr.io/cat")
	destRC := registry.Context{
		Name:           destRegName,
		ServiceAccount: "robot",
	}
	destRC2 := registry.Context{
		Name:           destRegName2,
		ServiceAccount: "robot",
	}
	srcRC := registry.Context{
		Name:           srcRegName,
		ServiceAccount: "robot",
		Src:            true,
	}
	registries := []registry.Context{destRC, srcRC, destRC2}

	registriesRebase := []registry.Context{
		{
			Name:           image.Registry("us.gcr.io/dog/some/subdir/path/foo"),
			ServiceAccount: "robot",
		},
		srcRC,
	}

	tests := []struct {
		name                  string
		inputM                schema.Manifest
		inputSc               SyncContext
		badReads              []image.Registry
		expectedReqs          CapturedRequests
		expectedFilteredClean bool
	}{
		{
			// TODO: Use quickcheck to ensure certain properties.
			"No promotion",
			schema.Manifest{},
			SyncContext{},
			nil,
			CapturedRequests{},
			true,
		},
		{
			"No promotion; tag is already promoted",
			schema.Manifest{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.9"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
					"gcr.io/bar": {
						"a": {
							"sha256:000": {"0.9"},
						},
						"b": {
							"sha256:111": {},
						},
					},
					"gcr.io/cat": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
				},
			},
			nil,
			CapturedRequests{},
			true,
		},
		{
			"No promotion; network errors reading from src registry for all images",
			schema.Manifest{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.9"},
						},
					},
					{
						Name: "b",
						Dmap: registry.DigestTags{
							"sha256:111": {"0.9"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"a": {
							"sha256:000": {"0.9"},
						},
						"b": {
							"sha256:111": {"0.9"},
						},
					},
				},
				InvIgnore: []image.Name{},
			},
			[]image.Registry{"gcr.io/foo/a", "gcr.io/foo/b", "gcr.io/foo/c"},
			CapturedRequests{},
			true,
		},
		{
			"Promote 1 tag; image digest does not exist in dest",
			schema.Manifest{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.9"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
					"gcr.io/bar": {
						"b": {
							"sha256:111": {},
						},
					},
					"gcr.io/cat": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
				},
			},
			nil,
			CapturedRequests{
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[0].Name,
					ServiceAccount: registries[0].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "0.9",
				}: 1,
			},
			true,
		},
		{
			"Promote 1 tag; image already exists in dest, but digest does not",
			schema.Manifest{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.9"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
					"gcr.io/bar": {
						"a": {
							"sha256:111": {},
						},
					},
					"gcr.io/cat": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
				},
			},
			nil,
			CapturedRequests{
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[0].Name,
					ServiceAccount: registries[0].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "0.9",
				}: 1,
			},
			true,
		},
		{
			"Promote 1 tag; tag already exists in dest but is pointing to a different digest (move tag)",
			schema.Manifest{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							// sha256:bad is a bad image uploaded by a
							// compromised account. "good" is a good tag that is
							// already known and used for this image "a" (and in
							// both gcr.io/bar and gcr.io/cat, point to a known
							// good digest, 600d.).
							"sha256:bad": {"good"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"a": {
							// Malicious image.
							"sha256:bad": {"some-other-tag"},
						},
					},
					"gcr.io/bar": {
						"a": {
							"sha256:bad":  {"some-other-tag"},
							"sha256:600d": {"good"},
						},
					},
					"gcr.io/cat": {
						"a": {
							"sha256:bad":  {"some-other-tag"},
							"sha256:600d": {"good"},
						},
					},
				},
			},
			nil,
			CapturedRequests{},
			false,
		},
		{
			"Promote 1 tag as a 'rebase'",
			schema.Manifest{
				Registries: registriesRebase,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.9"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
					"us.gcr.io/dog/some/subdir/path": {
						"a": {
							"sha256:111": {"0.8"},
						},
					},
				},
			},
			nil,
			CapturedRequests{
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registriesRebase[0].Name,
					ServiceAccount: registriesRebase[0].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "0.9",
				}: 1,
			},
			true,
		},
		{
			"Promote 1 digest (tagless promotion)",
			schema.Manifest{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"a": {
							"sha256:000": {},
						},
					},
					"gcr.io/bar": {
						"a": {
							// "bar" already has it
							"sha256:000": {},
						},
					},
					"gcr.io/cat": {
						"c": {
							"sha256:222": {},
						},
					},
				},
			},
			nil,
			CapturedRequests{
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[2].Name,
					ServiceAccount: registries[2].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "",
				}: 1,
			},
			true,
		},
		{
			"NOP; dest has extra tag, but NOP because -delete-extra-tags NOT specified",
			schema.Manifest{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.9"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
					"gcr.io/bar": {
						"a": {
							"sha256:000": {"0.9", "extra-tag"},
						},
					},
					"gcr.io/cat": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
				},
			},
			nil,
			CapturedRequests{},
			true,
		},
		{
			"NOP (src registry does not have any of the images we want to promote)",
			schema.Manifest{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"missing-from-src"},
							"sha256:333": {"0.8"},
						},
					},
					{
						Name: "b",
						Dmap: registry.DigestTags{
							"sha256:bbb": {"also-missing"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"c": {
							"sha256:000": {"0.9"},
						},
						"d": {
							"sha256:bbb": {"1.0"},
						},
					},
					"gcr.io/bar": {
						"a": {
							"sha256:333": {"0.8"},
						},
					},
				},
			},
			nil,
			CapturedRequests{},
			true,
		},
		{
			"Add 1 tag for 2 registries",
			schema.Manifest{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.9", "1.0"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
					"gcr.io/bar": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
					"gcr.io/cat": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
				},
			},
			nil,
			CapturedRequests{
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[0].Name,
					ServiceAccount: registries[0].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "1.0",
				}: 1,
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[2].Name,
					ServiceAccount: registries[2].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "1.0",
				}: 1,
			},
			true,
		},
		{
			"Add 1 tag for 1 registry",
			schema.Manifest{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.9", "1.0"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
					"gcr.io/bar": {
						"a": {
							"sha256:000": {"0.9"},
						},
					},
					"gcr.io/cat": {
						"a": {
							"sha256:000": {
								"0.9", "1.0", "extra-tag",
							},
						},
					},
				},
			},
			nil,
			CapturedRequests{
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[0].Name,
					ServiceAccount: registries[0].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "1.0",
				}: 1,
			},
			true,
		},
	}

	// captured is sort of a "global variable" because processRequestFake
	// closes over it.
	captured := make(CapturedRequests)
	processRequestFake := MkRequestCapturerFunc(&captured)

	for i := range tests {
		// Reset captured for each test.
		captured = make(CapturedRequests)
		srcReg, err := registry.GetSrcRegistry(registries)

		require.NoError(t, err)

		tests[i].inputSc.SrcRegistry = srcReg

		// Simulate bad network conditions.
		if tests[i].badReads != nil {
			for _, badRead := range tests[i].badReads {
				tests[i].inputSc.IgnoreFromPromotion(badRead)
			}
		}

		edges, err := ToPromotionEdges([]schema.Manifest{tests[i].inputM})
		require.NoError(t, err)

		filteredEdges, gotClean, err := tests[i].inputSc.FilterPromotionEdges(
			edges, false,
		)
		require.NoError(t, err)
		require.Equal(t, tests[i].expectedFilteredClean, gotClean)

		err = tests[i].inputSc.Promote(
			filteredEdges,
			processRequestFake,
		)
		require.NoError(t, err)
		require.Equal(t, tests[i].expectedReqs, captured)
	}
}

func TestExecRequests(t *testing.T) {
	sc := SyncContext{}

	destRC := registry.Context{
		Name:           image.Registry("gcr.io/bar"),
		ServiceAccount: "robot",
	}

	destRC2 := registry.Context{
		Name:           image.Registry("gcr.io/cat"),
		ServiceAccount: "robot",
	}

	srcRC := registry.Context{
		Name:           image.Registry("gcr.io/foo"),
		ServiceAccount: "robot",
		Src:            true,
	}

	registries := []registry.Context{destRC, srcRC, destRC2}

	edges, err := ToPromotionEdges(
		[]schema.Manifest{
			{
				Registries: registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.9"},
						},
					},
				},
				SrcRegistry: &srcRC,
			},
		},
	)
	require.NoError(t, err)

	promotionRequests := sc.BuildPopulateRequestsForPromotionEdges(
		edges,
	)

	var processRequestSuccess ProcessRequestFunc = func(req stream.ExternalRequest) (RequestResult, error) {
		{
			reqRes := RequestResult{Context: req}
			return reqRes, nil
		}
	}

	var processRequestError ProcessRequestFunc = func(req stream.ExternalRequest) (RequestResult, error) {
		{
			reqRes := RequestResult{Context: req}
			errs := make(Errors, 0)
			errs = append(errs, Error{
				Context: "Running TestExecRequests",
				Error:   errors.New("This request results in an error"),
			})
			reqRes.Errors = errs
			return reqRes, nil
		}
	}

	tests := []struct {
		name               string
		processRequestFn   func(req stream.ExternalRequest) (RequestResult, error)
		expectedErrorCount int
	}{
		{
			"Error tracking for successful promotion",
			processRequestSuccess,
			0,
		},
		{
			"Error tracking for promotion with errors",
			processRequestError,
			len(promotionRequests),
		},
	}

	for _, test := range tests {
		results := ForkJoin(10, promotionRequests, test.processRequestFn)

		var errorCount int
		for _, res := range results {
			errorCount += len(res.Output.Errors)
			if res.Error != nil {
				errorCount++
			}
		}
		require.Equal(t, test.expectedErrorCount, errorCount)
	}
}

func TestValidateEdges(t *testing.T) {
	srcRegName := image.Registry("gcr.io/src")
	dstRegName := image.Registry("gcr.io/dst")
	srcRegistry := registry.Context{
		Name:           srcRegName,
		ServiceAccount: "robot",
		Src:            true,
	}
	dstRegistry := registry.Context{
		Name:           dstRegName,
		ServiceAccount: "robot",
	}

	registries := []registry.Context{
		srcRegistry,
		dstRegistry,
	}

	tests := []struct {
		name     string
		inputM   schema.Manifest
		inputSc  SyncContext
		expected error
	}{
		{
			"No problems (nothing to promote)",
			schema.Manifest{
				SrcRegistry: &srcRegistry,
				Registries:  registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.0"},
						},
					},
				},
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/src": {
						"a": {
							"sha256:000": {"0.0"},
						},
					},
					"gcr.io/dst": {
						"a": {
							"sha256:000": {"0.0"},
						},
					},
				},
			},
			nil,
		},
		{
			"Promotion edges OK",
			schema.Manifest{
				SrcRegistry: &srcRegistry,
				Registries:  registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							"sha256:000": {"0.0"},
							// This is an image we want to promote. There are no
							// tag moves involved here, so it's OK.
							"sha256:111": {"1.0"},
						},
					},
				},
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/src": {
						"a": {
							"sha256:000": {},
							"sha256:111": {},
						},
					},
					"gcr.io/dst": {
						"a": {
							"sha256:000": {"0.0"},
						},
					},
				},
			},
			nil,
		},
		{
			"Tag move detected in promotion edge",
			schema.Manifest{
				SrcRegistry: &srcRegistry,
				Registries:  registries,
				Images: []registry.Image{
					{
						Name: "a",
						Dmap: registry.DigestTags{
							// The idea here is that we've already promoted
							// sha256:111 as tag 1.0, but we want to try to
							// retag 1.0 to the sha256:222 image instead. This
							// intent should result in an error.
							"sha256:222": {"1.0"},
						},
					},
				},
			},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/src": {
						"a": {
							"sha256:000": {},
							"sha256:111": {},
						},
					},
					"gcr.io/dst": {
						"a": {
							"sha256:000": {"0.0"},
							"sha256:111": {"1.0"},
						},
					},
				},
			},
			errors.New("[edge &{{gcr.io/src robot  true} {a 1.0} sha256:222 {gcr.io/dst robot  false} {a 1.0}}: tag '1.0' in dest points to sha256:111, not sha256:222 (as per the manifest), but tag moves are not supported; skipping]"),
		},
	}

	for i := range tests {
		edges, err := ToPromotionEdges([]schema.Manifest{tests[i].inputM})
		require.NoError(t, err)
		got := tests[i].inputSc.ValidateEdges(edges)
		require.Equal(t, tests[i].expected, got)
	}
}

func TestSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		input    registry.RegInvImage
		expected string
	}{
		{
			"Basic",
			registry.RegInvImage{
				"foo": {
					"sha256:111": {"one"},
					"sha256:fff": {"0.9", "0.5"},
					"sha256:abc": {"0.3", "0.2"},
				},
				"bar": {
					"sha256:000": {"0.8", "0.5", "0.9"},
				},
			},
			`- name: bar
  dmap:
    "sha256:000": ["0.5", "0.8", "0.9"]
- name: foo
  dmap:
    "sha256:111": ["one"]
    "sha256:abc": ["0.2", "0.3"]
    "sha256:fff": ["0.5", "0.9"]
`,
		},
	}

	for _, test := range tests {
		got := test.input.ToYAML(registry.YamlMarshalingOpts{})
		require.Equal(t, test.expected, got)
	}
}

func TestParseContainerParts(t *testing.T) {
	type ContainerParts struct {
		registry   string
		repository string
		err        error
	}

	shouldBeValid := []struct {
		input    string
		expected ContainerParts
	}{
		{
			"gcr.io/google-containers/foo",
			ContainerParts{
				"gcr.io/google-containers",
				"foo",
				nil,
			},
		},
		{
			"us.gcr.io/google-containers/foo",
			ContainerParts{
				"us.gcr.io/google-containers",
				"foo",
				nil,
			},
		},
		{
			"us.gcr.io/google-containers/foo/bar",
			ContainerParts{
				"us.gcr.io/google-containers",
				"foo/bar",
				nil,
			},
		},
		{
			"k8s.gcr.io/a/b/c",
			ContainerParts{
				"k8s.gcr.io",
				"a/b/c",
				nil,
			},
		},
	}

	for _, test := range shouldBeValid {
		registryName, repository, err := ParseContainerParts(test.input)
		got := ContainerParts{
			registryName,
			repository,
			err,
		}

		require.Equal(t, test.expected, got)
	}

	shouldBeInvalid := []struct {
		input    string
		expected ContainerParts
	}{
		{
			// Blank string.
			"",
			ContainerParts{
				"",
				"",
				fmt.Errorf("invalid string '%s'", ""),
			},
		},
		{
			// Bare domain..
			"gcr.io",
			ContainerParts{
				"",
				"",
				fmt.Errorf("invalid string '%s'", "gcr.io"),
			},
		},
		{
			// Another top-level name (missing image name).
			"gcr.io/google-containers",
			ContainerParts{
				"",
				"",
				fmt.Errorf("invalid string '%s'", "gcr.io/google-containers"),
			},
		},
		{
			// Naked vanity domain (missing image name).
			"k8s.gcr.io",
			ContainerParts{
				"",
				"",
				fmt.Errorf("invalid string '%s'", "k8s.gcr.io"),
			},
		},
		{
			// Double slash.
			"k8s.gcr.io//a/b",
			ContainerParts{
				"",
				"",
				fmt.Errorf("invalid string '%s'", "k8s.gcr.io//a/b"),
			},
		},
		{
			// Trailing slash.
			"k8s.gcr.io/a/b/",
			ContainerParts{
				"",
				"",
				fmt.Errorf("invalid string '%s'", "k8s.gcr.io/a/b/"),
			},
		},
	}

	for _, test := range shouldBeInvalid {
		registryName, repository, err := ParseContainerParts(test.input)
		got := ContainerParts{
			registryName,
			repository,
			err,
		}

		require.Equal(t, test.expected, got)
	}
}

func TestMatch(t *testing.T) {
	inputMfest := schema.Manifest{
		Registries: []registry.Context{
			{
				Name:           "gcr.io/foo-staging",
				ServiceAccount: "sa@robot.com",
				Src:            true,
			},
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
					"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {"1.0"},
				},
			},
		},
		Filepath: "a/promoter-manifest.yaml",
	}

	tests := []struct {
		name          string
		mfest         schema.Manifest
		gcrPayload    GCRPubSubPayload
		expectedMatch GcrPayloadMatch
	}{
		{
			"INSERT message contains both Digest and Tag",
			inputMfest,
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "us.gcr.io/some-prod/foo-controller@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				PQIN:   "us.gcr.io/some-prod/foo-controller:1.0",
			},
			GcrPayloadMatch{
				PathMatch:   true,
				DigestMatch: true,
				TagMatch:    true,
			},
		},
		{
			"INSERT message only contains Digest",
			inputMfest,
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "us.gcr.io/some-prod/foo-controller@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
			GcrPayloadMatch{
				PathMatch:   true,
				DigestMatch: true,
			},
		},
		{
			"INSERT's digest is not in Manifest (digest mismatch, but path matched)",
			inputMfest,
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "us.gcr.io/some-prod/foo-controller@sha256:000",
			},
			GcrPayloadMatch{
				PathMatch: true,
			},
		},
		{
			"INSERT's digest is not in Manifest (neither digest nor tag match, but path matched)",
			inputMfest,
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "us.gcr.io/some-prod/foo-controller@sha256:000",
				PQIN:   "us.gcr.io/some-prod/foo-controller:1.0",
			},
			GcrPayloadMatch{
				PathMatch: true,
			},
		},
		{
			"INSERT's digest is not in Manifest (tag specified, but tag mismatch)",
			inputMfest,
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "us.gcr.io/some-prod/foo-controller@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				PQIN:   "us.gcr.io/some-prod/foo-controller:white-powder",
			},
			GcrPayloadMatch{
				PathMatch:   true,
				DigestMatch: true,
				TagMismatch: true,
			},
		},
	}

	for _, test := range tests {
		err := test.gcrPayload.PopulateExtraFields()
		require.NoError(t, err)
		got := test.gcrPayload.Match(&test.mfest)
		require.Equal(t, test.expectedMatch, got)
	}
}

func TestPopulateExtraFields(t *testing.T) {
	shouldBeValid := []struct {
		name     string
		input    GCRPubSubPayload
		expected GCRPubSubPayload
	}{
		{
			"basic",
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "k8s.gcr.io/subproject/foo@sha256:000",
				PQIN:   "k8s.gcr.io/subproject/foo:1.0",
				Path:   "",
				Digest: "",
				Tag:    "",
			},
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "k8s.gcr.io/subproject/foo@sha256:000",
				PQIN:   "k8s.gcr.io/subproject/foo:1.0",
				Path:   "k8s.gcr.io/subproject/foo",
				Digest: "sha256:000",
				Tag:    "1.0",
			},
		},
		{
			"only FQIN",
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "k8s.gcr.io/subproject/foo@sha256:000",
				PQIN:   "",
				Path:   "",
				Digest: "",
				Tag:    "",
			},
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "k8s.gcr.io/subproject/foo@sha256:000",
				PQIN:   "",
				Path:   "k8s.gcr.io/subproject/foo",
				Digest: "sha256:000",
				Tag:    "",
			},
		},
		{
			"only PQIN",
			GCRPubSubPayload{
				Action: "DELETE",
				FQIN:   "",
				PQIN:   "k8s.gcr.io/subproject/foo:1.0",
				Path:   "",
				Digest: "",
				Tag:    "",
			},
			GCRPubSubPayload{
				Action: "DELETE",
				FQIN:   "",
				PQIN:   "k8s.gcr.io/subproject/foo:1.0",
				Path:   "k8s.gcr.io/subproject/foo",
				Digest: "",
				Tag:    "1.0",
			},
		},
	}

	for _, test := range shouldBeValid {
		err := test.input.PopulateExtraFields()
		require.NoError(t, err)

		got := test.input
		require.Equal(t, test.expected, got)
	}

	shouldBeInvalid := []struct {
		name     string
		input    GCRPubSubPayload
		expected error
	}{
		{
			"FQIN missing @-sign",
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "k8s.gcr.io/subproject/foosha256:000",
				PQIN:   "k8s.gcr.io/subproject/foo:1.0",
				Path:   "",
				Digest: "",
				Tag:    "",
			},
			errors.New("invalid FQIN: k8s.gcr.io/subproject/foosha256:000"),
		},
		{
			"PQIN missing colon",
			GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "k8s.gcr.io/subproject/foo@sha256:000",
				PQIN:   "k8s.gcr.io/subproject/foo1.0",
				Path:   "",
				Digest: "",
				Tag:    "",
			},
			errors.New("invalid PQIN: k8s.gcr.io/subproject/foo1.0"),
		},
	}

	for _, test := range shouldBeInvalid {
		got := test.input.PopulateExtraFields()
		require.EqualError(t, got, test.expected.Error())
	}
}

// Helper functions.

func bazelTestPath(testName string, paths ...string) string {
	prefix := []string{
		os.Getenv("PWD"),
		"inventory_test",
		testName,
	}

	return filepath.Join(append(prefix, paths...)...)
}
