package cmd

import (
	"context"
	"testing"

	"sigs.k8s.io/k8s-container-image-promoter/pkg/golden"
)

func TestHash(t *testing.T) {
	ctx := context.Background()

	var opt GenerateManifestOptions
	opt.PopulateDefaults()

	opt.BaseDir = "testdata/files"

	manifest, err := GenerateManifest(ctx, opt)
	if err != nil {
		t.Fatalf("failed to generate manifest: %v", err)
	}

	golden.AssertMatchesFile(t, manifest, "testdata/files-manifest.yaml")
}
