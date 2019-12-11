/*
Copyright 2019 The Kubernetes Authors.

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

package inventory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	cr "github.com/google/go-containerregistry/pkg/v1/types"
	"sigs.k8s.io/k8s-container-image-promoter/lib/json"
	"sigs.k8s.io/k8s-container-image-promoter/lib/stream"
)

func checkEqual(got, expected interface{}) error {
	if !reflect.DeepEqual(got, expected) {
		return fmt.Errorf(
			`<<<<<<< got (type %T)
%v
=======
%v
>>>>>>> expected (type %T)`,
			got,
			got,
			expected,
			expected)
	}
	return nil
}

func checkError(t *testing.T, err error, msg string) {
	if err != nil {
		fmt.Printf("\n%v", msg)
		fmt.Println(err)
		fmt.Println()
		t.Fail()
	}
}

type ParseJSONStreamResult struct {
	jsons json.Objects
	err   error
}

func TestReadJSONStream(t *testing.T) {
	var tests = []struct {
		name           string
		input          string
		expectedOutput ParseJSONStreamResult
	}{
		{
			"Blank input stream",
			`[]`,
			ParseJSONStreamResult{json.Objects{}, nil},
		},
		// The order of the maps matters.
		{
			"Simple case",
			`[
  {
    "name": "gcr.io/louhi-gke-k8s/addon-resizer"
  },
  {
    "name": "gcr.io/louhi-gke-k8s/pause"
  }
]`,
			ParseJSONStreamResult{
				json.Objects{
					{"name": "gcr.io/louhi-gke-k8s/addon-resizer"},
					{"name": "gcr.io/louhi-gke-k8s/pause"}},
				nil},
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
				errors.New("yaml: line 4: did not find expected node content")},
		},
	}

	// Test only the JSON unmarshalling logic.
	for _, test := range tests {
		var sr stream.Fake
		sr.Bytes = []byte(test.input)
		stdout, _, err := sr.Produce()

		// The fake should never error out when producing a stdout stream for
		// us.
		eqErr := checkEqual(err, nil)
		checkError(t, eqErr, fmt.Sprintf("Test: %v (Produce() err)\n",
			test.name))

		jsons, err := json.Consume(stdout)
		_ = sr.Close()

		// Check the error as well (at the very least, we can check that the
		// error was nil).
		eqErr = checkEqual(err, test.expectedOutput.err)
		checkError(t, eqErr, fmt.Sprintf("Test: %v (json.Consume() err)\n",
			test.name))

		got := jsons
		expected := test.expectedOutput.jsons
		eqErr = checkEqual(got, expected)
		checkError(t, eqErr, fmt.Sprintf("Test: %v (json)\n", test.name))
	}
}

func TestParseRegistryManifest(t *testing.T) {
	// TODO: Create a function to convert an Manifest to a YAML
	// representation, and vice-versa.
	//
	// TODO: Use property-based testing to test the fidelity of the conversion
	// (marshaling/unmarshaling) functions.
	var tests = []struct {
		name           string
		input          string
		expectedOutput Manifest
		expectedError  error
	}{
		{
			"Empty manifest (invalid)",
			``,
			Manifest{},
			fmt.Errorf(`'registries' field cannot be empty`),
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
			Manifest{
				Registries: []RegistryContext{
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

				Images: []Image{},
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
			Manifest{
				Registries: []RegistryContext{
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

				Images: []Image{
					{ImageName: "agave",
						Dmap: DigestTags{
							"sha256:aab34c5841987a1b133388fa9f27e7960c4b1307e2f9147dca407ba26af48a54": {"latest"},
						},
					},
					{ImageName: "banana",
						Dmap: DigestTags{
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
			Manifest{},
			fmt.Errorf("source registry must be set"),
		},
	}

	// Test only the JSON unmarshalling logic.
	for _, test := range tests {
		b := []byte(test.input)
		imageManifest, err := ParseManifestYAML(b)

		// Check the error as well (at the very least, we can check that the
		// error was nil).
		eqErr := checkEqual(err, test.expectedError)
		checkError(t, eqErr, fmt.Sprintf("Test: %v (error)\n", test.name))

		// There is nothing more to check if we expected a parse failure.
		if test.expectedError != nil {
			continue
		}

		got := imageManifest
		expected := test.expectedOutput
		eqErr = checkEqual(got, expected)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: %v (Manifest)\n", test.name))
	}
}

func TestParseManifestsFromDir(t *testing.T) {
	pwd := bazelTestPath("TestParseManifestsFromDir")

	var tests = []struct {
		name string
		// "input" is folder name, relative to the location of this source file.
		input          string
		expectedOutput []Manifest
		expectedError  error
	}{
		{
			"No manifests found (invalid)",
			"empty",
			[]Manifest{},
			fmt.Errorf("no manifests found in dir: %s/%s", pwd, "empty"),
		},
		{
			"Singleton (single manifest)",
			"singleton",
			[]Manifest{
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "foo-controller",
							Dmap: DigestTags{
								"sha256:c3d310f4741b3642497da8826e0986db5e02afc9777a2b8e668c8e41034128c1": {"1.0"},
							},
						},
					},
					filepath: "a/promoter-manifest.yaml",
				},
			},
			nil,
		},
		{
			"Basic (multiple manifests)",
			"basic",
			[]Manifest{
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "foo-controller",
							Dmap: DigestTags{
								"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {"1.0"},
							},
						},
					},
					filepath: "a/promoter-manifest.yaml"},
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "cat-controller",
							Dmap: DigestTags{
								"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc": {"1.0"},
							},
						},
					},
					filepath: "b/c/promoter-manifest.yaml"},
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "bar-controller",
							Dmap: DigestTags{
								"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": {"1.0"},
							},
						},
					},
					filepath: "b/promoter-manifest.yaml"},
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "qux-controller",
							Dmap: DigestTags{
								"sha256:0000000000000000000000000000000000000000000000000000000000000000": {"1.0"},
							},
						},
					},
					filepath: "promoter-manifest.yaml"},
			},
			nil,
		},
		{
			"Multiple (with 'rebase')",
			"multiple-rebases",
			[]Manifest{
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "foo-controller",
							Dmap: DigestTags{
								"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {"1.0"},
							},
						},
					},
					filepath: "a/promoter-manifest.yaml",
				},
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "bar-controller",
							Dmap: DigestTags{
								"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": {"1.0"},
							},
						},
					},
					filepath: "b/promoter-manifest.yaml",
				},
			},
			nil,
		},
		{
			"Basic (multiple thin manifests)",
			"basic-thin",
			[]Manifest{
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "foo-controller",
							Dmap: DigestTags{
								"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {"1.0"},
							},
						},
					},
					filepath: "thin-manifests/a/promoter-manifest.yaml"},
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "cat-controller",
							Dmap: DigestTags{
								"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc": {"1.0"},
							},
						},
					},
					filepath: "thin-manifests/b/c/promoter-manifest.yaml"},
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "bar-controller",
							Dmap: DigestTags{
								"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": {"1.0"},
							},
						},
					},
					filepath: "thin-manifests/b/promoter-manifest.yaml"},
				{
					Registries: []RegistryContext{
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
					Images: []Image{
						{ImageName: "qux-controller",
							Dmap: DigestTags{
								"sha256:0000000000000000000000000000000000000000000000000000000000000000": {"1.0"},
							},
						},
					},
					filepath: "thin-manifests/promoter-manifest.yaml"},
			},
			nil,
		},
	}

	for _, test := range tests {
		fixtureDir := bazelTestPath("TestParseManifestsFromDir", test.input)

		// Fixup expected filepaths to match bazel's testing directory.
		expectedModified := test.expectedOutput[:0]
		for _, mfest := range test.expectedOutput {
			mfest.filepath = filepath.Join(fixtureDir, mfest.filepath)
			expectedModified = append(expectedModified, mfest)
		}

		parseManifestFunc := ParseManifestFromFile
		if test.input == "basic-thin" {
			parseManifestFunc = ParseThinManifestFromFile
		}

		got, err := ParseManifestsFromDir(fixtureDir, parseManifestFunc)

		// Clear private fields (redundant data) that are calculated on-the-fly
		// (it's too verbose to include them here; besides, it's not what we're
		// testing).
		gotModified := got[:0]
		for _, mfest := range got {
			mfest.srcRegistry = nil
			gotModified = append(gotModified, mfest)
		}

		// Check the error as well (at the very least, we can check that the
		// error was nil).
		eqErr := checkEqual(err, test.expectedError)
		checkError(t, eqErr, fmt.Sprintf("Test: %v (error)\n", test.name))

		// There is nothing more to check if we expected a parse failure.
		if test.expectedError != nil {
			continue
		}

		eqErr = checkEqual(gotModified, test.expectedOutput)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: %v (Manifest)\n", test.name))
	}
}

func TestValidateManifestsFromDir(t *testing.T) {

	var shouldBeValid = []string{
		"basic",
		"singleton",
		"multiple-rebases",
		"overlapping-src-registries",
		"overlapping-destination-vertices-same-digest",
	}

	pwd := bazelTestPath("TestValidateManifestsFromDir")

	for _, testInput := range shouldBeValid {
		fixtureDir := filepath.Join(pwd, "valid", testInput)

		mfests, errParse := ParseManifestsFromDir(fixtureDir, ParseManifestFromFile)
		eqErr := checkEqual(errParse, nil)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' should be valid (ParseManifestsFromDir)\n", testInput))

		_, edgeErr := ToPromotionEdges(mfests)
		eqErr = checkEqual(edgeErr, nil)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' should be valid\n", testInput))

	}

	// nolint[golint]
	var shouldBeInvalid = []struct {
		dirName               string
		expectedParseError    error
		expectedValidateError error
		expectedEdgeError     error
	}{
		{
			"empty",
			fmt.Errorf("no manifests found in dir: %s", filepath.Join(pwd, "invalid/empty")),
			nil,
			nil,
		},
		{

			"overlapping-destination-vertices-different-digest",
			nil,
			nil,
			fmt.Errorf(
				"overlapping edges detected"),
		},
	}

	for _, test := range shouldBeInvalid {
		fixtureDir := bazelTestPath("TestValidateManifestsFromDir", "invalid", test.dirName)
		var eqErr error

		// It could be that a manifest, taken individually, failed on its own,
		// before we even get to ValidateManifestsFromDir(). So handle these
		// cases as well.
		mfests, errParse := ParseManifestsFromDir(fixtureDir, ParseManifestFromFile)
		eqErr = checkEqual(errParse, test.expectedParseError)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' should be invalid (ParseManifestsFromDir)\n", test.dirName))
		if errParse != nil {
			continue
		}

		_, edgeErr := ToPromotionEdges(mfests)
		eqErr = checkEqual(edgeErr, test.expectedEdgeError)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' should be invalid (ToPromotionEdges)\n", test.dirName))
	}
}

func TestParseImageDigest(t *testing.T) {
	var shouldBeValid = []string{
		`sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`,
		`sha256:0000000000000000000000000000000000000000000000000000000000000000`,
		`sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff`,
		`sha256:3243f6a8885a308d313198a2e03707344a4093822299f31d0082efa98ec4e6c8`,
	}

	for _, testInput := range shouldBeValid {
		d := Digest(testInput)
		got := validateDigest(d)
		eqErr := checkEqual(got, nil)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' should be valid\n", testInput))
	}

	var shouldBeInvalid = []string{
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
		d := Digest(testInput)
		got := validateDigest(d)
		eqErr := checkEqual(got, fmt.Errorf("invalid digest: %v", d))
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' should be invalid\n", testInput))
	}
}

func TestParseImageTag(t *testing.T) {
	var shouldBeValid = []string{
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
		tag := Tag(testInput)
		got := validateTag(tag)
		eqErr := checkEqual(got, nil)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' should be valid\n", testInput))
	}

	var shouldBeInvalid = []string{
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
		tag := Tag(testInput)
		got := validateTag(tag)
		eqErr := checkEqual(got, fmt.Errorf("invalid tag: %v", tag))
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' should be invalid\n", testInput))
	}
}

func TestValidateRegistryImagePath(t *testing.T) {
	//func validateRegistryImagePath(rip RegistryImagePath) error {
	var shouldBeValid = []string{
		`gcr.io/foo/bar`,
		`k8s.gcr.io/foo`,
		`staging-k8s.gcr.io/foo`,
		`staging-k8s.gcr.io/foo/bar/nested/path/image`,
	}

	for _, testInput := range shouldBeValid {
		rip := RegistryImagePath(testInput)
		got := validateRegistryImagePath(rip)
		eqErr := checkEqual(got, nil)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' should be valid\n", testInput))
	}

	var shouldBeInvalid = []string{
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
		got := validateRegistryImagePath(rip)
		eqErr := checkEqual(
			got, fmt.Errorf("invalid registry image path: %v", rip))
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' should be invalid\n", testInput))
	}

}

func TestSplitRegistryImagePath(t *testing.T) {
	knownRegistryNames := []RegistryName{
		`gcr.io/foo`,
		`us.gcr.io/foo`,
		`k8s.gcr.io`,
		`eu.gcr.io/foo/d`,
	}

	var tests = []struct {
		name                 string
		input                RegistryImagePath
		expectedRegistryName RegistryName
		expectedImageName    ImageName
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
		eqErr := checkEqual(rName, test.expectedRegistryName)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' failure (registry name mismatch)\n", test.input))
		eqErr = checkEqual(iName, test.expectedImageName)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' failure (image name mismatch)\n", test.input))
		eqErr = checkEqual(err, test.expectedErr)
		checkError(
			t,
			eqErr,
			fmt.Sprintf("Test: `%v' failure (error mismatch)\n", test.input))
	}
}

func TestCommandGeneration(t *testing.T) {
	destRC := RegistryContext{
		Name:           "gcr.io/foo",
		ServiceAccount: "robot"}
	var srcRegName RegistryName = "gcr.io/bar"
	var srcImageName ImageName = "baz"
	var destImageName ImageName = "baz"
	var digest Digest = "sha256:000"
	var tag Tag = "1.0"
	var tp TagOp

	testName := "GetDeleteCmd"
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
		"--format=json"}
	eqErr := checkEqual(got, expected)
	checkError(
		t,
		eqErr,
		fmt.Sprintf("Test: %v (cmd string)\n", testName))

	got = GetDeleteCmd(
		destRC,
		false,
		destImageName,
		digest,
		false)
	expected = []string{
		"gcloud",
		"container",
		"images",
		"delete",
		ToFQIN(destRC.Name, destImageName, digest),
		"--format=json"}
	eqErr = checkEqual(got, expected)
	checkError(
		t,
		eqErr,
		fmt.Sprintf("Test: %v (cmd string)\n", testName))

	testName = "GetWriteCmd (Delete)"
	tp = Delete
	got = GetWriteCmd(
		destRC,
		true,
		srcRegName,
		srcImageName,
		destImageName,
		digest,
		tag,
		tp)
	expected = []string{
		"gcloud",
		"--account=robot",
		"--quiet",
		"container",
		"images",
		"untag",
		ToPQIN(destRC.Name, destImageName, tag)}
	eqErr = checkEqual(got, expected)
	checkError(
		t,
		eqErr,
		fmt.Sprintf("Test: %v (cmd string)\n", testName))

	got = GetWriteCmd(
		destRC,
		false,
		srcRegName,
		srcImageName,
		destImageName,
		digest,
		tag,
		tp)
	expected = []string{
		"gcloud",
		"--quiet",
		"container",
		"images",
		"untag",
		ToPQIN(destRC.Name, destImageName, tag)}
	eqErr = checkEqual(got, expected)
	checkError(
		t,
		eqErr,
		fmt.Sprintf("Test: %v (cmd string)\n", testName))
}

// TestReadRegistries tests reading images and tags from a registry.
func TestReadRegistries(t *testing.T) {
	const fakeRegName RegistryName = "gcr.io/foo"

	var tests = []struct {
		name           string
		input          map[string]string
		expectedOutput RegInvImage
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
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "tag": [
        "latest"
      ],
      "timeCreatedMs": "1501774217070",
      "timeUploadedMs": "1552917295327"
    },
    "sha256:0519a83e8f217e33dd06fe7a7347444cfda5e2e29cf52aaa24755999cb104a4d": {
      "imageSizeBytes": "12875324",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
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
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
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
			RegInvImage{
				"addon-resizer": DigestTags{
					"sha256:b5b2d91319f049143806baeacc886f82f621e9a2550df856b11b5c22db4570a7": {"latest"},
					"sha256:0519a83e8f217e33dd06fe7a7347444cfda5e2e29cf52aaa24755999cb104a4d": {"1.0"}},
				"pause": DigestTags{
					"sha256:06fdf10aae2eeeac5a82c213e4693f82ab05b3b09b820fce95a7cac0bbdad534": {"v1.2.3"}}},
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
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "tag": [
        "latest"
      ],
      "timeCreatedMs": "1501774217070",
      "timeUploadedMs": "1552917295327"
    },
    "sha256:0519a83e8f217e33dd06fe7a7347444cfda5e2e29cf52aaa24755999cb104a4d": {
      "imageSizeBytes": "12875324",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
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
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
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
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
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
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
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
			RegInvImage{
				"addon-resizer": DigestTags{
					"sha256:b5b2d91319f049143806baeacc886f82f621e9a2550df856b11b5c22db4570a7": {"latest"},
					"sha256:0519a83e8f217e33dd06fe7a7347444cfda5e2e29cf52aaa24755999cb104a4d": {"1.0"}},
				"pause": DigestTags{
					"sha256:06fdf10aae2eeeac5a82c213e4693f82ab05b3b09b820fce95a7cac0bbdad534": {"v1.2.3"}},
				"pause/childLevel1": DigestTags{
					"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {"aaa"}},
				"pause/childLevel1/childLevel2": DigestTags{
					"sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff": {"fff"}}},
		},
	}
	for _, test := range tests {
		// Destination registry is a placeholder, because ReadImageNames acts on
		// 2 registries (src and dest) at once.
		rcs := []RegistryContext{
			{
				Name:           fakeRegName,
				ServiceAccount: "robot",
			},
		}
		sc := SyncContext{
			RegistryContexts: rcs,
			Inv:              map[RegistryName]RegInvImage{fakeRegName: nil},
			DigestMediaType:  make(DigestMediaType)}
		// test is used to pin the "test" variable from the outer "range"
		// scope (see scopelint).
		test := test
		mkFakeStream1 := func(sc *SyncContext, rc RegistryContext) stream.Producer {
			var sr stream.Fake

			_, domain, repoPath := GetTokenKeyDomainRepoPath(rc.Name)
			fakeHTTPBody, ok := test.input[domain+"/"+repoPath]
			if !ok {
				checkError(
					t,
					fmt.Errorf("could not read fakeHTTPBody"),
					fmt.Sprintf("Test: %v\n", test.name))
			}
			sr.Bytes = []byte(fakeHTTPBody)
			return &sr
		}
		sc.ReadRegistries(rcs, true, mkFakeStream1)
		got := sc.Inv[fakeRegName]
		expected := test.expectedOutput
		err := checkEqual(got, expected)
		checkError(t, err, fmt.Sprintf("Test: %v\n", test.name))
	}
}

// TestReadGManifestLists tests reading ManifestList information from GCR.
func TestReadGManifestLists(t *testing.T) {
	const fakeRegName RegistryName = "gcr.io/foo"

	var tests = []struct {
		name           string
		input          map[string]string
		expectedOutput ParentDigest
	}{
		{
			"Basic example",
			map[string]string{
				"gcr.io/foo/someImage": `{
   "schemaVersion": 2,
   "mediaType": "application/vnd.docker.distribution.manifest.list.v2+json",
   "manifests": [
      {
         "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
         "size": 739,
         "digest": "sha256:0bd88bcba94f800715fca33ffc4bde430646a7c797237313cbccdcdef9f80f2d",
         "platform": {
            "architecture": "amd64",
            "os": "linux"
         }
      },
      {
         "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
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
				"sha256:0ad4f92011b2fa5de88a6e6a2d8b97f38371246021c974760e5fc54b9b7069e5": "sha256:0000000000000000000000000000000000000000000000000000000000000000"},
		},
	}

	for _, test := range tests {
		// Destination registry is a placeholder, because ReadImageNames acts on
		// 2 registries (src and dest) at once.
		rcs := []RegistryContext{
			{
				Name:           fakeRegName,
				ServiceAccount: "robot",
			},
		}
		sc := SyncContext{
			RegistryContexts: rcs,
			Inv: map[RegistryName]RegInvImage{
				"gcr.io/foo": {
					"someImage": DigestTags{
						"sha256:0000000000000000000000000000000000000000000000000000000000000000": TagSlice{"1.0"}}}},
			DigestMediaType: DigestMediaType{
				"sha256:0000000000000000000000000000000000000000000000000000000000000000": cr.DockerManifestList},
			ParentDigest: make(ParentDigest)}
		// test is used to pin the "test" variable from the outer "range"
		// scope (see scopelint).
		test := test
		mkFakeStream1 := func(sc *SyncContext, gmlc GCRManifestListContext) stream.Producer {
			var sr stream.Fake

			_, domain, repoPath := GetTokenKeyDomainRepoPath(gmlc.RegistryContext.Name)
			fakeHTTPBody, ok := test.input[domain+"/"+repoPath+"/"+string(gmlc.ImageName)]
			if !ok {
				checkError(
					t,
					fmt.Errorf("could not read fakeHTTPBody"),
					fmt.Sprintf("Test: %v\n", test.name))
			}
			sr.Bytes = []byte(fakeHTTPBody)
			return &sr
		}
		sc.ReadGCRManifestLists(mkFakeStream1)
		got := sc.ParentDigest
		expected := test.expectedOutput
		err := checkEqual(got, expected)
		checkError(t, err, fmt.Sprintf("Test: %v\n", test.name))
	}
}

func TestGetTokenKeyDomainRepoPath(t *testing.T) {
	type TokenKeyDomainRepoPath [3]string
	var tests = []struct {
		name     string
		input    RegistryName
		expected TokenKeyDomainRepoPath
	}{
		{
			"basic",
			"gcr.io/foo/bar",
			[3]string{"gcr.io/foo", "gcr.io", "foo/bar"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			tokenKey, domain, repoPath := GetTokenKeyDomainRepoPath(test.input)

			err := checkEqual(tokenKey, test.expected[0])
			checkError(t, err, "(tokenKey)\n")

			err = checkEqual(domain, test.expected[1])
			checkError(t, err, "(domain)\n")

			err = checkEqual(repoPath, test.expected[2])
			checkError(t, err, "(repoPath)\n")
		})
	}
}

func TestSetManipulationsRegistryInventories(t *testing.T) {
	var tests = []struct {
		name           string
		input1         RegInvImage
		input2         RegInvImage
		op             func(a, b RegInvImage) RegInvImage
		expectedOutput RegInvImage
	}{
		{
			"Set Minus",
			RegInvImage{
				"foo": DigestTags{
					"sha256:abc": []Tag{"1.0", "latest"}},
				"bar": DigestTags{
					"sha256:def": []Tag{"0.9"}},
			},
			RegInvImage{
				"foo": DigestTags{
					"sha256:abc": []Tag{"1.0", "latest"}},
				"bar": DigestTags{
					"sha256:def": []Tag{"0.9"}},
			},
			RegInvImage.Minus,
			RegInvImage{},
		},
		{
			"Set Union",
			RegInvImage{
				"foo": DigestTags{
					"sha256:abc": []Tag{"1.0", "latest"}},
				"bar": DigestTags{
					"sha256:def": []Tag{"0.9"}},
			},
			RegInvImage{
				"apple": DigestTags{
					"sha256:abc": []Tag{"1.0", "latest"}},
				"banana": DigestTags{
					"sha256:def": []Tag{"0.9"}},
			},
			RegInvImage.Union,
			RegInvImage{
				"foo": DigestTags{
					"sha256:abc": []Tag{"1.0", "latest"}},
				"bar": DigestTags{
					"sha256:def": []Tag{"0.9"}},
				"apple": DigestTags{
					"sha256:abc": []Tag{"1.0", "latest"}},
				"banana": DigestTags{
					"sha256:def": []Tag{"0.9"}},
			},
		},
	}

	for _, test := range tests {
		got := test.op(test.input1, test.input2)
		expected := test.expectedOutput
		err := checkEqual(got, expected)
		checkError(t, err, fmt.Sprintf("Test: %v\n", test.name))
	}
}

func TestSetManipulationsTags(t *testing.T) {
	var tests = []struct {
		name           string
		input1         TagSlice
		input2         TagSlice
		op             func(a, b TagSlice) TagSet
		expectedOutput TagSet
	}{
		{
			"Set Minus (both blank)",
			TagSlice{},
			TagSlice{},
			TagSlice.Minus,
			TagSet{},
		},
		{
			"Set Minus (first blank)",
			TagSlice{},
			TagSlice{"a"},
			TagSlice.Minus,
			TagSet{},
		},
		{
			"Set Minus (second blank)",
			TagSlice{"a", "b"},
			TagSlice{},
			TagSlice.Minus,
			TagSet{"a": nil, "b": nil},
		},
		{
			"Set Minus",
			TagSlice{"a", "b"},
			TagSlice{"b"},
			TagSlice.Minus,
			TagSet{"a": nil},
		},
		{
			"Set Union (both blank)",
			TagSlice{},
			TagSlice{},
			TagSlice.Union,
			TagSet{},
		},
		{
			"Set Union (first blank)",
			TagSlice{},
			TagSlice{"a"},
			TagSlice.Union,
			TagSet{"a": nil},
		},
		{
			"Set Union (second blank)",
			TagSlice{"a"},
			TagSlice{},
			TagSlice.Union,
			TagSet{"a": nil},
		},
		{
			"Set Union",
			TagSlice{"a", "c"},
			TagSlice{"b", "d"},
			TagSlice.Union,
			TagSet{"a": nil, "b": nil, "c": nil, "d": nil},
		},
		{
			"Set Intersection (no intersection)",
			TagSlice{"a"},
			TagSlice{"b"},
			TagSlice.Intersection,
			TagSet{},
		},
		{
			"Set Intersection (some intersection)",
			TagSlice{"a", "b"},
			TagSlice{"b", "c"},
			TagSlice.Intersection,
			TagSet{"b": nil},
		},
	}

	for _, test := range tests {
		got := test.op(test.input1, test.input2)
		expected := test.expectedOutput
		err := checkEqual(got, expected)
		checkError(t, err, fmt.Sprintf("Test: %v\n", test.name))
	}
}

func TestSetManipulationsRegInvImageTag(t *testing.T) {
	var tests = []struct {
		name           string
		input1         RegInvImageTag
		input2         RegInvImageTag
		op             func(a, b RegInvImageTag) RegInvImageTag
		expectedOutput RegInvImageTag
	}{
		{
			"Set Minus (both blank)",
			RegInvImageTag{},
			RegInvImageTag{},
			RegInvImageTag.Minus,
			RegInvImageTag{},
		},
		{
			"Set Minus (first blank)",
			RegInvImageTag{},
			RegInvImageTag{
				ImageTag{"pear", "latest"}: "123"},
			RegInvImageTag.Minus,
			RegInvImageTag{},
		},
		{
			"Set Minus (second blank)",
			RegInvImageTag{
				ImageTag{"pear", "latest"}: "123"},
			RegInvImageTag{},
			RegInvImageTag.Minus,
			RegInvImageTag{
				ImageTag{"pear", "latest"}: "123"},
		},
		{
			"Set Intersection (both blank)",
			RegInvImageTag{},
			RegInvImageTag{},
			RegInvImageTag.Intersection,
			RegInvImageTag{},
		},
		{
			"Set Intersection (first blank)",
			RegInvImageTag{},
			RegInvImageTag{
				ImageTag{"pear", "latest"}: "123"},
			RegInvImageTag.Intersection,
			RegInvImageTag{},
		},
		{
			"Set Intersection (second blank)",
			RegInvImageTag{
				ImageTag{"pear", "latest"}: "123"},
			RegInvImageTag{},
			RegInvImageTag.Intersection,
			RegInvImageTag{},
		},
		{
			"Set Intersection (no intersection)",
			RegInvImageTag{
				ImageTag{"pear", "latest"}: "123"},
			RegInvImageTag{
				ImageTag{"pear", "1.0"}: "123"},
			RegInvImageTag.Intersection,
			RegInvImageTag{},
		},
		{
			"Set Intersection (some intersection)",
			RegInvImageTag{
				ImageTag{"pear", "latest"}: "this-is-kept",
				ImageTag{"pear", "1.0"}:    "123"},
			RegInvImageTag{
				ImageTag{"pear", "latest"}: "this-is-lost",
				ImageTag{"foo", "2.0"}:     "def"},
			// The intersection code throws out the second value, because it
			// treats a Map as a Set (and doesn't care about preserving
			// information for the key's value).
			RegInvImageTag.Intersection,
			RegInvImageTag{
				ImageTag{"pear", "latest"}: "this-is-kept"},
		},
	}

	for _, test := range tests {
		got := test.op(test.input1, test.input2)
		expected := test.expectedOutput
		err := checkEqual(got, expected)
		checkError(t, err, fmt.Sprintf("Test: %v\n", test.name))
	}
}

func TestToPromotionEdges(t *testing.T) {
	srcRegName := RegistryName("gcr.io/foo")
	destRegName := RegistryName("gcr.io/bar")
	destRegName2 := RegistryName("gcr.io/cat")
	destRC := RegistryContext{
		Name:           destRegName,
		ServiceAccount: "robot",
	}
	destRC2 := RegistryContext{
		Name:           destRegName2,
		ServiceAccount: "robot",
	}
	srcRC := RegistryContext{
		Name:           srcRegName,
		ServiceAccount: "robot",
		Src:            true,
	}
	registries1 := []RegistryContext{destRC, srcRC}
	registries2 := []RegistryContext{destRC, srcRC, destRC2}

	sc := SyncContext{
		Inv: MasterInventory{
			"gcr.io/foo": RegInvImage{
				"a": DigestTags{
					"sha256:000": TagSlice{"0.9"}},
				"c": DigestTags{
					"sha256:222": TagSlice{"2.0"},
					"sha256:333": TagSlice{"3.0"}}},
			"gcr.io/bar": RegInvImage{
				"a": DigestTags{
					"sha256:000": TagSlice{"0.9"}},
				"b": DigestTags{
					"sha256:111": TagSlice{}},
				"c": DigestTags{
					"sha256:222": TagSlice{"2.0"},
					"sha256:333": TagSlice{"3.0"}}},
			"gcr.io/cat": RegInvImage{
				"a": DigestTags{
					"sha256:000": TagSlice{"0.9"}},
				"c": DigestTags{
					"sha256:222": TagSlice{"2.0"},
					"sha256:333": TagSlice{"3.0"}}}}}

	var tests = []struct {
		name                  string
		input                 []Manifest
		expectedInitial       map[PromotionEdge]interface{}
		expectedInitialErr    error
		expectedFiltered      map[PromotionEdge]interface{}
		expectedFilteredClean bool
	}{
		{
			"Basic case (1 new edge; already promoted)",
			[]Manifest{
				{
					Registries: registries1,
					Images: []Image{
						{
							ImageName: "a",
							Dmap: DigestTags{
								"sha256:000": TagSlice{"0.9"}}}},
					srcRegistry: &srcRC},
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"}}: nil,
			},
			nil,
			make(map[PromotionEdge]interface{}),
			true,
		},
		{
			"Basic case (2 new edges; already promoted)",
			[]Manifest{
				{
					Registries: registries2,
					Images: []Image{
						{
							ImageName: "a",
							Dmap: DigestTags{
								"sha256:000": TagSlice{"0.9"}}}},
					srcRegistry: &srcRC},
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"}}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"},
					Digest:      "sha256:000",
					DstRegistry: destRC2,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"}}: nil,
			},
			nil,
			make(map[PromotionEdge]interface{}),
			true,
		},
		{
			"Tag move (tag swap image c:2.0 and c:3.0)",
			[]Manifest{
				{
					Registries: registries2,
					Images: []Image{
						{
							ImageName: "c",
							Dmap: DigestTags{
								"sha256:222": TagSlice{"3.0"},
								"sha256:333": TagSlice{"2.0"}}}},
					srcRegistry: &srcRC},
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "c",
						Tag:       "2.0"},
					Digest:      "sha256:333",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "c",
						Tag:       "2.0"}}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "c",
						Tag:       "3.0"},
					Digest:      "sha256:222",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "c",
						Tag:       "3.0"}}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "c",
						Tag:       "2.0"},
					Digest:      "sha256:333",
					DstRegistry: destRC2,
					DstImageTag: ImageTag{
						ImageName: "c",
						Tag:       "2.0"}}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "c",
						Tag:       "3.0"},
					Digest:      "sha256:222",
					DstRegistry: destRC2,
					DstImageTag: ImageTag{
						ImageName: "c",
						Tag:       "3.0"}}: nil,
			},
			nil,
			make(map[PromotionEdge]interface{}),
			false,
		},
	}

	for _, test := range tests {
		// Finalize Manifests.
		for i := range test.input {
			// Skip errors.
			_ = test.input[i].finalize()
		}
		got, gotErr := ToPromotionEdges(test.input)
		err := checkEqual(got, test.expectedInitial)
		checkError(t, err, fmt.Sprintf("checkError: test: %v (ToPromotionEdges)\n", test.name))

		err = checkEqual(gotErr, test.expectedInitialErr)
		checkError(t, err, fmt.Sprintf("checkError: test: %v (ToPromotionEdges (error mismatch)\n", test.name))

		got, gotClean := sc.getPromotionCandidates(got)
		err = checkEqual(got, test.expectedFiltered)
		checkError(t, err, fmt.Sprintf("checkError: test: %v (getPromotionCandidates)\n", test.name))

		err = checkEqual(gotClean, test.expectedFilteredClean)
		checkError(t, err, fmt.Sprintf("checkError: test: %v (getPromotionCandidates (cleanliness mismatch)\n", test.name))
	}
}

func TestCheckOverlappingEdges(t *testing.T) {
	srcRegName := RegistryName("gcr.io/foo")
	destRegName := RegistryName("gcr.io/bar")
	destRC := RegistryContext{
		Name:           destRegName,
		ServiceAccount: "robot",
	}
	srcRC := RegistryContext{
		Name:           srcRegName,
		ServiceAccount: "robot",
		Src:            true,
	}

	var tests = []struct {
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
						ImageName: "a",
						Tag:       "0.9"},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"}}: nil,
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"}}: nil,
			},
			nil,
		},
		{
			"Basic case (two edges, no overlapping edges)",
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"}}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "b",
						Tag:       "0.9"},
					Digest:      "sha256:111",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "b",
						Tag:       "0.9"}}: nil,
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"}}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "b",
						Tag:       "0.9"},
					Digest:      "sha256:111",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "b",
						Tag:       "0.9"}}: nil,
			},
			nil,
		},
		{
			"Basic case (two edges, overlapped)",
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"}}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "b",
						Tag:       "0.9"},
					Digest:      "sha256:111",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"}}: nil,
			},
			nil,
			fmt.Errorf("overlapping edges detected"),
		},
		{
			"Basic case (two tagless edges (different digests, same PQIN), no overlap)",
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       ""}}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "b",
						Tag:       "0.9"},
					Digest:      "sha256:111",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       ""}}: nil,
			},
			map[PromotionEdge]interface{}{
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "a",
						Tag:       "0.9"},
					Digest:      "sha256:000",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       ""}}: nil,
				{
					SrcRegistry: srcRC,
					SrcImageTag: ImageTag{
						ImageName: "b",
						Tag:       "0.9"},
					Digest:      "sha256:111",
					DstRegistry: destRC,
					DstImageTag: ImageTag{
						ImageName: "a",
						Tag:       ""}}: nil,
			},
			nil,
		},
	}

	for _, test := range tests {
		got, gotErr := checkOverlappingEdges(test.input)
		err := checkEqual(got, test.expected)
		checkError(t, err, fmt.Sprintf("checkError: test: %v\n", test.name))

		err = checkEqual(gotErr, test.expectedErr)
		checkError(t, err, fmt.Sprintf("checkError: test: %v (error mismatch)\n", test.name))
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
	srcRegName := RegistryName("gcr.io/foo")
	destRegName := RegistryName("gcr.io/bar")
	destRegName2 := RegistryName("gcr.io/cat")
	destRC := RegistryContext{
		Name:           destRegName,
		ServiceAccount: "robot",
	}
	destRC2 := RegistryContext{
		Name:           destRegName2,
		ServiceAccount: "robot",
	}
	srcRC := RegistryContext{
		Name:           srcRegName,
		ServiceAccount: "robot",
		Src:            true,
	}
	registries := []RegistryContext{destRC, srcRC, destRC2}

	registriesRebase := []RegistryContext{
		{
			Name:           RegistryName("us.gcr.io/dog/some/subdir/path/foo"),
			ServiceAccount: "robot",
		},
		srcRC}

	var tests = []struct {
		name                  string
		inputM                Manifest
		inputSc               SyncContext
		badReads              []RegistryName
		expectedReqs          CapturedRequests
		expectedFilteredClean bool
	}{
		{
			// TODO: Use quickcheck to ensure certain properties.
			"No promotion",
			Manifest{},
			SyncContext{},
			nil,
			CapturedRequests{},
			true,
		},
		{
			"No promotion; tag is already promoted",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}},
						"b": DigestTags{
							"sha256:111": TagSlice{}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			nil,
			CapturedRequests{},
			true,
		},
		{
			"No promotion; network errors reading from src registry for all images",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					{
						ImageName: "b",
						Dmap: DigestTags{
							"sha256:111": TagSlice{"0.9"}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}},
						"b": DigestTags{
							"sha256:111": TagSlice{"0.9"}}}},
				InvIgnore: []ImageName{}},
			[]RegistryName{"gcr.io/foo/a", "gcr.io/foo/b", "gcr.io/foo/c"},
			CapturedRequests{},
			true,
		},
		{
			"Promote 1 tag; image digest does not exist in dest",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"b": DigestTags{
							"sha256:111": TagSlice{}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			nil,
			CapturedRequests{PromotionRequest{
				TagOp:          Add,
				RegistrySrc:    srcRegName,
				RegistryDest:   registries[0].Name,
				ServiceAccount: registries[0].ServiceAccount,
				ImageNameSrc:   "a",
				ImageNameDest:  "a",
				Digest:         "sha256:000",
				Tag:            "0.9"}: 1},
			true,
		},
		{
			"Promote 1 tag; image already exists in dest, but digest does not",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:111": TagSlice{}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			nil,
			CapturedRequests{PromotionRequest{
				TagOp:          Add,
				RegistrySrc:    srcRegName,
				RegistryDest:   registries[0].Name,
				ServiceAccount: registries[0].ServiceAccount,
				ImageNameSrc:   "a",
				ImageNameDest:  "a",
				Digest:         "sha256:000",
				Tag:            "0.9"}: 1},
			true,
		},
		{
			"Promote 1 tag; tag already exists in dest but is pointing to a different digest (move tag)",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							// sha256:bad is a bad image uploaded by a
							// compromised account. "good" is a good tag that is
							// already known and used for this image "a" (and in
							// both gcr.io/bar and gcr.io/cat, point to a known
							// good digest, 600d.).
							"sha256:bad": TagSlice{"good"}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							// Malicious image.
							"sha256:bad": TagSlice{"some-other-tag"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:bad":  TagSlice{"some-other-tag"},
							"sha256:600d": TagSlice{"good"}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:bad":  TagSlice{"some-other-tag"},
							"sha256:600d": TagSlice{"good"}}}}},
			nil,
			CapturedRequests{},
			false,
		},
		{
			"Promote 1 tag as a 'rebase'",
			Manifest{
				Registries: registriesRebase,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"us.gcr.io/dog/some/subdir/path": RegInvImage{
						"a": DigestTags{
							"sha256:111": TagSlice{"0.8"}}},
				}},
			nil,
			CapturedRequests{PromotionRequest{
				TagOp:          Add,
				RegistrySrc:    srcRegName,
				RegistryDest:   registriesRebase[0].Name,
				ServiceAccount: registriesRebase[0].ServiceAccount,
				ImageNameSrc:   "a",
				ImageNameDest:  "a",
				Digest:         "sha256:000",
				Tag:            "0.9"}: 1},
			true,
		},
		{
			"Promote 1 digest (tagless promotion)",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							// "bar" already has it
							"sha256:000": TagSlice{}}},
					"gcr.io/cat": RegInvImage{
						"c": DigestTags{
							"sha256:222": TagSlice{}}}}},
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
					Tag:            ""}: 1,
			},
			true,
		},
		{
			"NOP; dest has extra tag, but NOP because -delete-extra-tags NOT specified",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9", "extra-tag"}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			nil,
			CapturedRequests{},
			true,
		},
		{
			"NOP (src registry does not have any of the images we want to promote)",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"missing-from-src"},
							"sha256:333": TagSlice{"0.8"},
						}},
					{
						ImageName: "b",
						Dmap: DigestTags{
							"sha256:bbb": TagSlice{"also-missing"}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"c": DigestTags{
							"sha256:000": TagSlice{"0.9"}},
						"d": DigestTags{
							"sha256:bbb": TagSlice{"1.0"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:333": TagSlice{"0.8"}}}}},
			nil,
			CapturedRequests{},
			true,
		},
		{
			"Add 1 tag for 2 registries",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9", "1.0"}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
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
					Tag:            "1.0"}: 1,
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[2].Name,
					ServiceAccount: registries[2].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "1.0"}: 1,
			},
			true,
		},
		{
			"Add 1 tag for 1 registry",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9", "1.0"}}}},
				srcRegistry: &srcRC},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{
								"0.9", "1.0", "extra-tag"}}}}},
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
					Tag:            "1.0"}: 1,
			},
			true,
		},
	}

	// captured is sort of a "global variable" because processRequestFake
	// closes over it.
	captured := make(CapturedRequests)
	processRequestFake := MkRequestCapturer(&captured)

	nopStream := func(
		srcRegistry RegistryName,
		srcImageName ImageName,
		rc RegistryContext,
		destImageName ImageName,
		digest Digest,
		tag Tag,
		tp TagOp) stream.Producer {

		// We don't even need a stream producer, because we are not creating
		// subprocesses that generate JSON or any other output; the vanilla
		// "mkReq" in Promote() already stores all the info we need to check.
		return nil
	}

	for _, test := range tests {

		// Reset captured for each test.
		captured = make(CapturedRequests)
		srcReg, err := getSrcRegistry(registries)
		checkError(t, err,
			fmt.Sprintf("checkError (srcReg): test: %v\n", test.name))
		checkError(t, err,
			fmt.Sprintf("checkError (rd): test: %v\n", test.name))
		test.inputSc.SrcRegistry = srcReg

		// Simulate bad network conditions.
		if test.badReads != nil {
			for _, badRead := range test.badReads {
				test.inputSc.IgnoreFromPromotion(badRead)
			}
		}

		edges, err := ToPromotionEdges([]Manifest{test.inputM})
		eqErr := checkEqual(err, nil)
		checkError(t, eqErr, fmt.Sprintf("Test: %v: (unexpected error getting promotion edges)\n", test.name))

		filteredEdges, gotClean := test.inputSc.FilterPromotionEdges(
			edges,
			false)
		err = checkEqual(gotClean, test.expectedFilteredClean)
		checkError(t, err, fmt.Sprintf("checkError: test: %v (edge filtering cleanliness mismatch)\n", test.name))

		test.inputSc.Promote(
			filteredEdges,
			nopStream,
			&processRequestFake)

		err = checkEqual(captured, test.expectedReqs)
		checkError(t, err, fmt.Sprintf("checkError: test: %v\n", test.name))
	}
}

func TestGarbageCollection(t *testing.T) {
	srcRegName := RegistryName("gcr.io/foo")
	destRegName := RegistryName("gcr.io/bar")
	destRegName2 := RegistryName("gcr.io/cat")
	registries := []RegistryContext{
		{
			Name:           srcRegName,
			ServiceAccount: "robot",
			Src:            true,
		},
		{
			Name:           destRegName,
			ServiceAccount: "robot",
		},
		{
			Name:           destRegName2,
			ServiceAccount: "robot",
		},
	}
	var tests = []struct {
		name         string
		inputM       Manifest
		inputSc      SyncContext
		expectedReqs CapturedRequests
	}{
		{
			"No garbage collection (no empty digests)",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"missing-from-src"},
							"sha256:333": TagSlice{"0.8"},
						}},
					{
						ImageName: "b",
						Dmap: DigestTags{
							"sha256:bbb": TagSlice{"also-missing"}}}}},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"c": DigestTags{
							"sha256:000": TagSlice{"0.9"}},
						"d": DigestTags{
							"sha256:bbb": TagSlice{"1.0"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:333": TagSlice{"0.8"}}}}},
			CapturedRequests{},
		},
		{
			"Simple garbage collection (delete ALL images in dest that are untagged))",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"missing-from-src"},
							"sha256:333": TagSlice{"0.8"},
						}},
					{
						ImageName: "b",
						Dmap: DigestTags{
							"sha256:bbb": TagSlice{"also-missing"}}}}},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"c": DigestTags{
							"sha256:000": nil},
						"d": DigestTags{
							"sha256:bbb": nil}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							// NOTE: this is skipping the first step where we
							// delete extra tags away (-delete-extra-tags).
							"sha256:111": nil},
						"z": DigestTags{
							"sha256:000": nil},
					}}},
			CapturedRequests{
				PromotionRequest{
					TagOp:          Delete,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[1].Name,
					ServiceAccount: registries[1].ServiceAccount,
					ImageNameSrc:   "",
					ImageNameDest:  "a",
					Digest:         "sha256:111",
					Tag:            ""}: 1,
				PromotionRequest{
					TagOp:          Delete,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[1].Name,
					ServiceAccount: registries[1].ServiceAccount,
					ImageNameSrc:   "",
					ImageNameDest:  "z",
					Digest:         "sha256:000",
					Tag:            ""}: 1,
			},
		},
	}

	captured := make(CapturedRequests)

	var processRequestFake ProcessRequest = func(
		sc *SyncContext,
		reqs chan stream.ExternalRequest,
		errs chan<- RequestResult,
		wg *sync.WaitGroup,
		mutex *sync.Mutex) {

		for req := range reqs {
			pr := req.RequestParams.(PromotionRequest)
			mutex.Lock()
			if _, ok := captured[pr]; ok {
				captured[pr]++
			} else {
				captured[pr] = 1
			}
			mutex.Unlock()
			wg.Add(-1)
		}
	}

	for _, test := range tests {
		// Reset captured for each test.
		captured = make(CapturedRequests)
		nopStream := func(
			destRC RegistryContext,
			imageName ImageName,
			digest Digest) stream.Producer {
			return nil
		}
		srcReg, err := getSrcRegistry(registries)
		checkError(t, err,
			fmt.Sprintf("checkError (srcReg): test: %v\n", test.name))
		test.inputSc.SrcRegistry = srcReg
		test.inputSc.GarbageCollect(test.inputM, nopStream, &processRequestFake)

		err = checkEqual(captured, test.expectedReqs)
		checkError(t, err, fmt.Sprintf("checkError: test: %v\n", test.name))
	}
}

func TestGarbageCollectionMulti(t *testing.T) {
	srcRegName := RegistryName("gcr.io/src")
	destRegName1 := RegistryName("gcr.io/dest1")
	destRegName2 := RegistryName("gcr.io/dest2")
	destRC := RegistryContext{
		Name:           destRegName1,
		ServiceAccount: "robotDest1",
	}
	destRC2 := RegistryContext{
		Name:           destRegName2,
		ServiceAccount: "robotDest2",
	}
	srcRC := RegistryContext{
		Name:           srcRegName,
		ServiceAccount: "robotSrc",
		Src:            true,
	}
	registries := []RegistryContext{srcRC, destRC, destRC2}
	var tests = []struct {
		name         string
		inputM       Manifest
		inputSc      SyncContext
		expectedReqs CapturedRequests
	}{
		{
			"Simple garbage collection (delete ALL images in all dests that are untagged))",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"missing-from-src"},
							"sha256:333": TagSlice{"0.8"},
						}},
					{
						ImageName: "b",
						Dmap: DigestTags{
							"sha256:bbb": TagSlice{"also-missing"}}}}},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/src": RegInvImage{
						"c": DigestTags{
							"sha256:000": nil},
						"d": DigestTags{
							"sha256:bbb": nil}},
					"gcr.io/dest1": RegInvImage{
						"a": DigestTags{
							"sha256:111": nil},
						"z": DigestTags{
							"sha256:222": nil}},
					"gcr.io/dest2": RegInvImage{
						"a": DigestTags{
							"sha256:123": nil},
						"b": DigestTags{
							"sha256:444": nil}},
				}},
			CapturedRequests{
				PromotionRequest{
					TagOp:          Delete,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[1].Name,
					ServiceAccount: registries[1].ServiceAccount,
					ImageNameSrc:   "",
					ImageNameDest:  "a",
					Digest:         "sha256:111",
					Tag:            ""}: 1,
				PromotionRequest{
					TagOp:          Delete,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[1].Name,
					ServiceAccount: registries[1].ServiceAccount,
					ImageNameSrc:   "",
					ImageNameDest:  "z",
					Digest:         "sha256:222",
					Tag:            ""}: 1,
				PromotionRequest{
					TagOp:          Delete,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[2].Name,
					ServiceAccount: registries[2].ServiceAccount,
					ImageNameSrc:   "",
					ImageNameDest:  "a",
					Digest:         "sha256:123",
					Tag:            ""}: 1,
				PromotionRequest{
					TagOp:          Delete,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[2].Name,
					ServiceAccount: registries[2].ServiceAccount,
					ImageNameSrc:   "",
					ImageNameDest:  "b",
					Digest:         "sha256:444",
					Tag:            ""}: 1,
			},
		},
	}

	captured := make(CapturedRequests)

	var processRequestFake ProcessRequest = func(
		sc *SyncContext,
		reqs chan stream.ExternalRequest,
		errs chan<- RequestResult,
		wg *sync.WaitGroup,
		mutex *sync.Mutex) {

		for req := range reqs {
			pr := req.RequestParams.(PromotionRequest)
			mutex.Lock()
			if _, ok := captured[pr]; ok {
				captured[pr]++
			} else {
				captured[pr] = 1
			}
			mutex.Unlock()
			wg.Add(-1)
		}
	}

	for _, test := range tests {
		// Reset captured for each test.
		captured = make(CapturedRequests)
		nopStream := func(
			destRC RegistryContext,
			imageName ImageName,
			digest Digest) stream.Producer {
			return nil
		}
		srcReg, err := getSrcRegistry(registries)
		checkError(t, err,
			fmt.Sprintf("checkError (srcReg): test: %v\n", test.name))
		test.inputSc.SrcRegistry = srcReg
		test.inputSc.GarbageCollect(test.inputM, nopStream, &processRequestFake)

		err = checkEqual(captured, test.expectedReqs)
		checkError(t, err, fmt.Sprintf("checkError: test: %v\n", test.name))
	}
}

func TestSnapshot(t *testing.T) {
	var tests = []struct {
		name     string
		input    RegInvImage
		expected string
	}{
		{
			"Basic",
			RegInvImage{
				"foo": DigestTags{
					"sha256:fff": TagSlice{"0.9", "0.5"},
					"sha256:abc": TagSlice{"0.3", "0.2"},
				},
				"bar": DigestTags{
					"sha256:000": TagSlice{"0.8", "0.5", "0.9"}},
			},
			`- name: bar
  dmap:
    sha256:000:
    - 0.5
    - 0.8
    - 0.9
- name: foo
  dmap:
    sha256:abc:
    - 0.2
    - 0.3
    sha256:fff:
    - 0.5
    - 0.9
`,
		},
	}

	for _, test := range tests {
		gotYAML := test.input.ToYAML()
		err := checkEqual(gotYAML, test.expected)
		checkError(t, err, fmt.Sprintf("checkError: test: %v\n", test.name))
	}
}

func TestParseContainerParts(t *testing.T) {
	type ContainerParts struct {
		registry   string
		repository string
		err        error
	}

	var shouldBeValid = []struct {
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
		registry, repository, err := ParseContainerParts(test.input)
		got := ContainerParts{
			registry,
			repository,
			err}
		errEqual := checkEqual(got, test.expected)
		checkError(t, errEqual, "checkError: test: shouldBeValid\n")
	}

	var shouldBeInValid = []struct {
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

	for _, test := range shouldBeInValid {
		registry, repository, err := ParseContainerParts(test.input)
		got := ContainerParts{
			registry,
			repository,
			err}
		errEqual := checkEqual(got, test.expected)
		checkError(t, errEqual, "checkError: test: shouldBeInValid\n")
	}
}

// Helper functions.

func bazelTestPath(testName string, paths ...string) string {
	prefix := []string{
		os.Getenv("PWD"),
		"inventory_test",
		testName}
	return filepath.Join(append(prefix, paths...)...)
}
