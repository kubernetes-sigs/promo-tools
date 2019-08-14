package file

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"sigs.k8s.io/k8s-container-image-promoter/pkg/cmd"
	"sigs.k8s.io/yaml"
)

const ProdBucket = "gs://k8s-cip-test-prod"
const StagingBucket = "gs://k8s-staging-cip-test"

func TestPromote(t *testing.T) {
	for _, bucket := range []string{StagingBucket, ProdBucket} {
		files, err := ListFiles(bucket)
		if err != nil {
			t.Fatalf("failed to dump bucket %s: %v", bucket, err)
		}

		if err := DeleteAllFiles(files); err != nil {
			t.Fatalf("failed to empty bucket %s: %v", bucket, err)
		}
	}

	// We upload a file into the prod bucket, outside of the subdirectory - it should be untouched
	EnsureFile(t, ProdBucket+"/index.html", []byte("<html><body>k8s-cip-test-prod</body></html>"))

	for i := 1; i < 100; i += 10 {
		b := make([]byte, i*10000)
		for j := 0; j < len(b); j++ {
			b[j] = byte(i)
		}

		name := fmt.Sprintf("%d/%d.bin", i%7, i)
		EnsureFile(t, StagingBucket+"/"+name, b)
	}

	DumpMustMatch(t, StagingBucket, "tests/before-stage.yaml")
	DumpMustMatch(t, ProdBucket, "tests/before-prod.yaml")

	MustPromote(t, "tests/promotion1.yaml")

	DumpMustMatch(t, StagingBucket, "tests/after-promotion1-stage.yaml")
	DumpMustMatch(t, ProdBucket, "tests/after-promotion1-prod.yaml")
}

func EnsureFile(t *testing.T, url string, contents []byte) {
	err := UploadFile(url, contents)
	if err != nil {
		t.Fatalf("failed to upload file %s: %v", url, err)
	}
}

func MustPromote(t *testing.T, manifestPath string) {
	var options cmd.PromoteFilesOptions
	options.PopulateDefaults()

	options.ManifestPath = manifestPath
	options.UseServiceAccount = true
	options.DryRun = false

	ctx := context.TODO()

	if err := cmd.RunPromoteFiles(ctx, options); err != nil {
		t.Fatalf("failed to run promotion %s: %v",
			manifestPath, err)
	}
}

func DumpMustMatch(t *testing.T, baseURL string, goldenPath string) {
	info, err := ListFiles(baseURL)
	if err != nil {
		t.Fatalf("failed to dump %q: %v", baseURL, err)
	}

	y, err := yaml.Marshal(info)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	FileContentsMustMatch(t, goldenPath, y)
}

func FileContentsMustMatch(t *testing.T, p string, expectedBytes []byte) {
	expected := string(expectedBytes)

	if os.Getenv("UPDATE_GOLDEN_OUTPUT") != "" {
		if err := ioutil.WriteFile(p, expectedBytes, 0644); err != nil {
			t.Errorf("error writing file %q: %v", p, err)
		}
	}

	actualBytes, err := ioutil.ReadFile(p)
	if err != nil {
		t.Fatalf("error reading file %s: %v", p, err)
	}

	actual := string(actualBytes)

	if actual == expected {
		return
	}

	t.Errorf("mismatch in for %s\nactual: %s\nexpected: %s",
		p,
		actual,
		expected)
}
