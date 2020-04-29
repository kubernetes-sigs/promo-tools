package cmd_test

import (
	"context"
	"io/ioutil"
	"os"
	"testing"

	"k8s.io/utils/diff"
	"sigs.k8s.io/yaml"

	"sigs.k8s.io/k8s-container-image-promoter/pkg/cmd"
)

func TestHash(t *testing.T) {
	ctx := context.Background()

	var opt cmd.GenerateManifestOptions
	opt.PopulateDefaults()

	opt.BaseDir = "testdata/files"

	manifest, err := cmd.GenerateManifest(ctx, opt)
	if err != nil {
		t.Fatalf("failed to generate manifest: %v", err)
	}

	manifestYAML, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatalf("error serializing manifest: %v", err)
	}

	AssertMatchesFile(t, string(manifestYAML), "testdata/files-manifest.yaml")
}

// AssertMatchesFile verifies that the contents of p match actual.
//
//  We break this out into a file because we also support the
//  UPDATE_EXPECTED_OUTPUT magic env var. When that env var is
//  set, we will write the actual output to the expected file, which
//  is very handy when making bigger changes.  The intention of these
//  tests is to make the changes explicit, particularly in code
//  review, not to force manual updates.
func AssertMatchesFile(t *testing.T, actual string, p string) {
	b, err := ioutil.ReadFile(p)
	if err != nil {
		if os.Getenv("UPDATE_EXPECTED_OUTPUT") == "" {
			t.Fatalf("error reading file %q: %v", p, err)
		}
	}

	expected := string(b)

	if actual != expected {
		if os.Getenv("UPDATE_EXPECTED_OUTPUT") != "" {
			if err := ioutil.WriteFile(p, []byte(actual), 0644); err != nil {
				t.Fatalf("error writing file %q: %v", p, err)
			}
		}
		t.Errorf("actual did not match expected; diff=%s", diff.StringDiff(actual, expected))
	}
}
