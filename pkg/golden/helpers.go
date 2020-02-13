package golden

import (
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"k8s.io/utils/diff"
	"sigs.k8s.io/yaml"
)

// AssertMatchesFile verifies that the contents of p match actual.
//
//  We break this out into a file because we also support the
//  UPDATE_EXPECTED_OUTPUT magic env var. When that env var is
//  set, we will write the actual output to the expected file, which
//  is very handy when making bigger changes.  The intention of these
//  tests is to make the changes explicit, particularly in code
//  review, not to force manual updates.
func AssertMatchesFile(t *testing.T, actual interface{}, p string) {
	actualYAML := ""
	switch actual := actual.(type) {
	case string:
		actualYAML = actual
	default:
		y, err := yaml.Marshal(actual)
		if err != nil {
			t.Fatalf("error serializing: %v", err)
		}
		actualYAML = string(y)
	}

	// Normalize whitespace and keep git happy
	actualYAML = strings.TrimSpace(actualYAML) + "\n"

	b, err := ioutil.ReadFile(p)
	if err != nil {
		if os.Getenv("UPDATE_EXPECTED_OUTPUT") == "" {
			t.Fatalf("error reading file %q: %v", p, err)
		}
	}

	expected := string(b)

	if actualYAML != expected {
		if os.Getenv("UPDATE_EXPECTED_OUTPUT") != "" {
			if err := ioutil.WriteFile(p, []byte(actualYAML), 0644); err != nil {
				t.Fatalf("error writing file %q: %v", p, err)
			}
		}
		t.Errorf("actual did not match expected; diff=%s", diff.StringDiff(actualYAML, expected))
	}
}
