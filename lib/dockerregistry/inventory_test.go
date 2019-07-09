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
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/google"
	"sigs.k8s.io/k8s-container-image-promoter/lib/json"
	"sigs.k8s.io/k8s-container-image-promoter/lib/stream"
)

func checkEqual(got, expected interface{}) error {
	if !reflect.DeepEqual(got, expected) {
		return fmt.Errorf(
			"got (type %T):\n%v\nexpected (type %T):\n%v",
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
			// nolint[lll]
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
						Name: "gcr.io/bar",
						// nolint[lll]
						ServiceAccount: "foobar@google-containers.iam.gserviceaccount.com",
					},
					{
						Name: "gcr.io/foo",
						// nolint[lll]
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
			// nolint[lll]
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
						Name: "gcr.io/bar",
						// nolint[lll]
						ServiceAccount: "foobar@google-containers.iam.gserviceaccount.com",
					},
					{
						Name: "gcr.io/foo",
						// nolint[lll]
						ServiceAccount: "src@google-containers.iam.gserviceaccount.com",
						Src:            true,
					},
				},

				Images: []Image{
					{ImageName: "agave",
						Dmap: DigestTags{
							// nolint[lll]
							"sha256:aab34c5841987a1b133388fa9f27e7960c4b1307e2f9147dca407ba26af48a54": {"latest"},
						},
					},
					{ImageName: "banana",
						Dmap: DigestTags{
							// nolint[lll]
							"sha256:07353f7b26327f0d933515a22b1de587b040d3d85c464ea299c1b9f242529326": {"1.8.3"},
						},
					},
				},
			},
			nil,
		},
		{
			"Missing src registry in registries (invalid)",
			// nolint[lll]
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
			// nolint[lll]
			fmt.Errorf("source registry must be set"),
		},
	}

	// nolint[lll]
	// Test only the JSON unmarshalling logic.
	for _, test := range tests {
		b := []byte(test.input)
		imageManifest, err := ParseManifest(b)

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

		// Perform some extra testing for the `renames` field.
		if test.name == "Basic manifest" {
			shouldBeValid := []string{
				// Change the image path.
				`renames:
- ["gcr.io/foo/banana", "gcr.io/bar/some/subdir/banana"]`,
				// Change the base image name. This is allowed.
				`renames:
- ["gcr.io/foo/banana", "gcr.io/bar/carrot"]`,
			}
			for _, validInput := range shouldBeValid {
				b := []byte(test.input + validInput)
				_, err := ParseManifest(b)
				checkError(
					t,
					err,
					fmt.Sprintf(
						"Test: %v: (renames field parse failure (should be valid)\n",
						test.name+": "+validInput))
			}

			// nolint[lll]
			shouldBeInvalid := []struct {
				name  string
				input string
				err   error
			}{
				{
					name: "Only 1 path (need at least 2)",
					input: `renames:
- ["gcr.io/foo/banana"]`,
					err: fmt.Errorf("a rename entry must have at least 2 paths, one for the source, another for at least 1 dest registry"),
				},
				{
					name: "Multiple renames for a single registry",
					input: `renames:
- ["gcr.io/foo/banana", "gcr.io/bar/subdir/A/banana", "gcr.io/bar/subdir/B/banana"]`,
					err: fmt.Errorf("multiple renames found for registry 'gcr.io/bar' in 'renames', for image subdir/B/banana"),
				},
				{
					name: "Unknown registry",
					input: `renames:
- ["gcr.io/foo/banana", "gcr.io/cat/subdir/A/banana"]`,
					err: fmt.Errorf(`could not determine registry name for 'gcr.io/cat/subdir/A/banana'
unknown registry '' in 'renames' (not defined in 'registries')`),
				},
				{
					name: "No source registry",
					input: `renames:
- ["gcr.io/bar/banana", "gcr.io/cat/subdir/A/banana"]`,
					err: fmt.Errorf(`could not determine registry name for 'gcr.io/cat/subdir/A/banana'
unknown registry '' in 'renames' (not defined in 'registries')
could not find source registry in '[gcr.io/bar/banana gcr.io/cat/subdir/A/banana]'`),
				},
				{
					name: "Redundant rename",
					input: `renames:
- ["gcr.io/bar/banana", "gcr.io/foo/banana"]`,
					err: fmt.Errorf(`redundant rename for [gcr.io/bar/banana gcr.io/foo/banana]`),
				},
			}
			for _, invalid := range shouldBeInvalid {
				b := []byte(test.input + invalid.input)
				_, got := ParseManifest(b)
				eqErr := checkEqual(got, invalid.err)
				checkError(
					t,
					eqErr,
					fmt.Sprintf(
						"Test: %v: (renames field parse failure)\n",
						test.name+": "+invalid.name))
			}
		}
	}
}

func TestParseImageDigest(t *testing.T) {
	// nolint[lll]
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

	// nolint[lll]
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
	// nolint[lll]
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

	// nolint[lll]
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
	// nolint[lll]
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

	// nolint[lll]
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
	// nolint[lll]
	knownRegistryNames := []RegistryName{
		`gcr.io/foo`,
		`us.gcr.io/foo`,
		`k8s.gcr.io`,
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
			`vanity GCR`,
			`k8s.gcr.io/a/b/c`,
			`k8s.gcr.io`,
			`a/b/c`,
			nil,
		},
	}
	// nolint[lll]
	for _, test := range tests {
		rName, iName, err := splitRegistryImagePath(test.input, knownRegistryNames)
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
	var destImageName ImageName = "baz" // simple case (no rename)
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

	testName = "GetWriteCmd (Add)"
	tp = Add
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
		"--verbosity=debug",
		"container",
		"images",
		"add-tag",
		ToFQIN(srcRegName, destImageName, digest),
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
		"--verbosity=debug",
		"container",
		"images",
		"add-tag",
		ToFQIN(srcRegName, destImageName, digest),
		ToPQIN(destRC.Name, destImageName, tag)}
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

// TestReadRepository tests reading images and tags from a registry.
func TestReadRepository(t *testing.T) {
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
			// nolint[lll]
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
			// nolint[lll]
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
		sc := SyncContext{
			RegistryContexts: []RegistryContext{
				{
					Name:           fakeRegName,
					ServiceAccount: "robot",
				},
			},
			Inv:             map[RegistryName]RegInvImage{fakeRegName: nil},
			DigestMediaType: make(DigestMediaType)}
		// test is used to pin the "test" variable from the outer "range"
		// scope (see scopelint).
		test := test
		tagFetcherFake := func(sc *SyncContext, rc RegistryContext) (*google.Tags, error) {
			_, domain, repoPath := GetTokenKeyDomainRepoPath(rc.Name)
			fakeHTTPBody, ok := test.input[domain+"/"+repoPath]
			if !ok {
				checkError(
					t,
					fmt.Errorf("could not read fakeHTTPBody"),
					fmt.Sprintf("Test: %v\n", test.name))
			}

			r := bytes.NewReader([]byte(fakeHTTPBody))
			tags, err := extractRegistryTags(r)
			if err != nil {
				return nil, err
			}
			return tags, nil

		}
		if err := sc.ReadRepository(tagFetcherFake); err != nil {
			t.Fatalf("error reading from repository: %v", err)
		}

		got := sc.Inv[fakeRegName]
		expected := test.expectedOutput
		err := checkEqual(got, expected)
		checkError(t, err, fmt.Sprintf("Test: %v\n", test.name))
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

func TestDenormalizeRenames(t *testing.T) {
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
	registries := []RegistryContext{srcRC, destRC, destRC2}
	var tests = []struct {
		name           string
		input          Manifest
		expectedOutput RenamesDenormalized
	}{
		{
			"Simple case: 1 rename",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}},
				Renames: []Rename{
					[]RegistryImagePath{
						"gcr.io/foo/a",
						"gcr.io/bar/some/subdir/a"}}},
			RenamesDenormalized{
				"gcr.io/foo/a": map[RegistryName]ImageName{
					"gcr.io/bar": "some/subdir/a"},
				"gcr.io/bar/some/subdir/a": map[RegistryName]ImageName{
					"gcr.io/foo": "a"}},
		},
		{
			"2 renames, 2 different dest repos",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}},
				Renames: []Rename{
					[]RegistryImagePath{
						"gcr.io/foo/a",
						"gcr.io/bar/some/subdir/a",
						"gcr.io/cat/different/path/a",
					}}},
			RenamesDenormalized{
				"gcr.io/foo/a": map[RegistryName]ImageName{
					"gcr.io/bar": "some/subdir/a",
					"gcr.io/cat": "different/path/a"},
				"gcr.io/bar/some/subdir/a": map[RegistryName]ImageName{
					"gcr.io/foo": "a",
					"gcr.io/cat": "different/path/a",
				},
				"gcr.io/cat/different/path/a": map[RegistryName]ImageName{
					"gcr.io/foo": "a",
					"gcr.io/bar": "some/subdir/a",
				}},
		},
	}

	for _, test := range tests {
		got, err := DenormalizeRenames(test.input, srcRegName)
		checkError(t, err, fmt.Sprintf(
			"Test (denormalization): %v\n", test.name))
		expected := test.expectedOutput
		err = checkEqual(got, expected)
		checkError(t, err, fmt.Sprintf(
			"Test (equality): %v\n", test.name))
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

// TestPromotion is the most important test as it simulates the main job of the
// promoter. There should be a fake "handler" that can execute stateful changes
// to a fake GCR. Then it's just a matter of comparing the input GCR states +
// manifest, and then comparing what the output GCR states look like.
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
	// nolint[dup]
	var tests = []struct {
		name         string
		inputM       Manifest
		inputSc      SyncContext
		expectedReqs CapturedRequests
	}{
		{
			// TODO: Use quickcheck to ensure certain properties.
			"No promotion",
			Manifest{},
			SyncContext{},
			CapturedRequests{},
		},
		{
			"No promotion; tag is already promoted",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
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
			CapturedRequests{},
		},
		{
			"Promote 1 tag; image digest does not exist in dest",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
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
			CapturedRequests{PromotionRequest{
				TagOp:          Add,
				RegistrySrc:    srcRegName,
				RegistryDest:   registries[0].Name,
				ServiceAccount: registries[0].ServiceAccount,
				ImageNameSrc:   "a",
				ImageNameDest:  "a",
				Digest:         "sha256:000",
				Tag:            "0.9"}: 1},
		},
		{
			"Promote 1 tag; image already exists in dest, but digest does not",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
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
			CapturedRequests{PromotionRequest{
				TagOp:          Add,
				RegistrySrc:    srcRegName,
				RegistryDest:   registries[0].Name,
				ServiceAccount: registries[0].ServiceAccount,
				ImageNameSrc:   "a",
				ImageNameDest:  "a",
				Digest:         "sha256:000",
				Tag:            "0.9"}: 1},
		},
		{
			// nolint[lll]
			"Promote 1 tag; tag already exists in dest but is pointing to a different digest (move tag)",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:111": TagSlice{"0.9"}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			CapturedRequests{PromotionRequest{
				TagOp:          Move,
				RegistrySrc:    srcRegName,
				RegistryDest:   registries[0].Name,
				ServiceAccount: registries[0].ServiceAccount,
				ImageNameSrc:   "a",
				ImageNameDest:  "a",
				Digest:         "sha256:000",
				DigestOld:      "sha256:111",
				Tag:            "0.9"}: 1},
		},
		{
			// nolint[lll]
			"Promote 1 tag via rename",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}},
				Renames: []Rename{
					[]RegistryImagePath{
						"gcr.io/foo/a",
						"gcr.io/bar/some/subdir/path/a"}}},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"some/subdir/path/a": DigestTags{
							"sha256:111": TagSlice{"0.8"}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			CapturedRequests{PromotionRequest{
				TagOp:          Add,
				RegistrySrc:    srcRegName,
				RegistryDest:   registries[0].Name,
				ServiceAccount: registries[0].ServiceAccount,
				ImageNameSrc:   "a",
				ImageNameDest:  "some/subdir/path/a",
				Digest:         "sha256:000",
				Tag:            "0.9"}: 1},
		},
		{
			// nolint[lll]
			"Promote 1 tag via rename (move image)",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}},
				Renames: []Rename{
					[]RegistryImagePath{
						"gcr.io/foo/a",
						"gcr.io/bar/some/subdir/path/a"}}},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"some/subdir/path/a": DigestTags{
							"sha256:111": TagSlice{"0.9"}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			CapturedRequests{PromotionRequest{
				TagOp:          Move,
				RegistrySrc:    srcRegName,
				RegistryDest:   registries[0].Name,
				ServiceAccount: registries[0].ServiceAccount,
				ImageNameSrc:   "a",
				ImageNameDest:  "some/subdir/path/a",
				Digest:         "sha256:000",
				DigestOld:      "sha256:111",
				Tag:            "0.9"}: 1},
		},
		{
			// nolint[lll]
			"Promote 1 tag via rename (1 dest should be renamed, another should share the same name as src)",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}},
				Renames: []Rename{
					[]RegistryImagePath{
						"gcr.io/foo/a",
						"gcr.io/bar/some/subdir/path/a"}}},
			SyncContext{
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"some/subdir/path/a": DigestTags{
							"sha256:111": TagSlice{"0.8"}}},
					"gcr.io/cat": RegInvImage{
						"b": DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			CapturedRequests{
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[0].Name,
					ServiceAccount: registries[0].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "some/subdir/path/a",
					Digest:         "sha256:000",
					Tag:            "0.9"}: 1,
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[2].Name,
					ServiceAccount: registries[2].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "0.9"}: 1,
			},
		},
		{
			// nolint[lll]
			"NOP; dest has extra tag, but NOP because -delete-extra-tags NOT specified",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			SyncContext{
				DeleteExtraTags: false,
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
			CapturedRequests{},
		},
		{
			// nolint[lll]
			"Delete 1 tag; dest has extra tag (if -delete-extra-tags specified)",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			SyncContext{
				DeleteExtraTags: true,
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
			CapturedRequests{PromotionRequest{
				TagOp:          Delete,
				RegistrySrc:    srcRegName,
				RegistryDest:   registries[0].Name,
				ServiceAccount: registries[0].ServiceAccount,
				ImageNameSrc:   "a",
				ImageNameDest:  "a",
				Digest:         "sha256:000",
				Tag:            "extra-tag"}: 1},
		},
		{
			// nolint[lll]
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
		rd, err := DenormalizeRenames(test.inputM, srcReg.Name)
		checkError(t, err,
			fmt.Sprintf("checkError (rd): test: %v\n", test.name))
		test.inputSc.RenamesDenormalized = rd
		test.inputSc.SrcRegistry = srcReg
		test.inputSc.Promote(
			test.inputM,
			nopStream,
			&processRequestFake)
		err = checkEqual(captured, test.expectedReqs)
		checkError(t, err, fmt.Sprintf("checkError: test: %v\n", test.name))
	}
}

func TestPromotionMulti(t *testing.T) {
	srcRegName := RegistryName("gcr.io/foo")
	destRegName1 := RegistryName("gcr.io/bar")
	destRegName2 := RegistryName("gcr.io/qux")
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
			// nolint[lll]
			"Add 1 tag for 2 registries",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9", "1.0"}}}}},
			SyncContext{
				DeleteExtraTags: true,
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/qux": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}}}},
			CapturedRequests{
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[1].Name,
					ServiceAccount: registries[1].ServiceAccount,
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
		},
		{
			// nolint[lll]
			"Add 1 tag for 1 registry, but remove a tag for another",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"0.9", "1.0"}}}}},
			SyncContext{
				DeleteExtraTags: true,
				Inv: MasterInventory{
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/bar": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{"0.9"}}},
					"gcr.io/qux": RegInvImage{
						"a": DigestTags{
							"sha256:000": TagSlice{
								"0.9", "1.0", "extra-tag"}}}}},
			CapturedRequests{
				PromotionRequest{
					TagOp:          Add,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[1].Name,
					ServiceAccount: registries[1].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "1.0"}: 1,
				PromotionRequest{
					TagOp:          Delete,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[2].Name,
					ServiceAccount: registries[2].ServiceAccount,
					ImageNameSrc:   "a",
					ImageNameDest:  "a",
					Digest:         "sha256:000",
					Tag:            "extra-tag"}: 1,
			},
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
		test.inputSc.SrcRegistry = srcReg
		test.inputSc.Promote(
			test.inputM,
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
			// nolint[lll]
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
		{
			// nolint[lll]
			"Garbage collection with renames present (only garbage-collect *renamed* paths in dest)",
			Manifest{
				Registries: registries,
				Images: []Image{
					{
						ImageName: "a",
						Dmap: DigestTags{
							"sha256:000": TagSlice{"000"},
							"sha256:333": TagSlice{"333"},
						}},
					{
						ImageName: "b",
						Dmap: DigestTags{
							"sha256:bbb": TagSlice{"bbb"}}}},
				Renames: []Rename{
					[]RegistryImagePath{
						"gcr.io/foo/a",
						"gcr.io/bar/some/subdir/path/a"}}},
			SyncContext{
				Inv: MasterInventory{
					// Source repo has both images accounted for in the manifest
					// but also images extraneouc of the manifest. These
					// extraneous images should not affect garbage-collection at
					// all.
					"gcr.io/foo": RegInvImage{
						"a": DigestTags{
							"sha256:000": nil,
							"sha256:333": nil,
						},
						"b": DigestTags{
							"sha256:bbb": nil},
						"c": DigestTags{
							"sha256:000": nil},
						"d": DigestTags{
							"sha256:bbb": nil}},
					"gcr.io/bar": RegInvImage{
						"some/subdir/path/a": DigestTags{
							"sha256:000": TagSlice{"000"},
							"sha256:111": nil,
						},
						"b": DigestTags{
							"sha256:333": TagSlice{"bbb"}}},
					"gcr.io/cat": RegInvImage{
						"a": DigestTags{
							"sha256:fff": nil},
						"b": DigestTags{
							"sha256:bbb": TagSlice{"bbb"}}}}},
			CapturedRequests{
				// Delete an extra tag from the renamed path.
				PromotionRequest{
					TagOp:          Delete,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[1].Name,
					ServiceAccount: registries[1].ServiceAccount,
					ImageNameSrc:   "",
					ImageNameDest:  "some/subdir/path/a",
					Digest:         "sha256:111",
					Tag:            ""}: 1,
				// Delete a tag on a non-renamed  path (different dest)}.
				PromotionRequest{
					TagOp:          Delete,
					RegistrySrc:    srcRegName,
					RegistryDest:   registries[2].Name,
					ServiceAccount: registries[2].ServiceAccount,
					ImageNameSrc:   "",
					ImageNameDest:  "a",
					Digest:         "sha256:fff",
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
			// nolint[lll]
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
