package filepromoter

import (
	"context"
	"sort"
	"testing"

	"sigs.k8s.io/k8s-container-image-promoter/pkg/golden"
)

func TestListFiles(t *testing.T) {
	ctx := context.Background()

	s := &fsSyncFilestore{basedir: "testdata/files"}

	files, err := s.ListFiles(ctx)
	if err != nil {
		t.Fatalf("error listing files: %v", err)
	}

	var l []*syncFileInfo
	for _, f := range files {
		l = append(l, f)
	}

	sort.Slice(l, func(i, j int) bool {
		return l[i].AbsolutePath < l[j].AbsolutePath
	})

	golden.AssertMatchesFile(t, l, "testdata/expected-listfiles.yaml")
}
