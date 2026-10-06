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

package imagepromoter

import (
	"testing"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

func TestSourcePoliciesLevels(t *testing.T) {
	t.Parallel()

	// A parent and a nested manifest with equal policies name the same
	// image differently, so their levels can differ for it.
	const (
		digest = image.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

		// The image as the nested and the parent policy name it.
		nestedName = "img"
		parentName = "b/img"
	)

	edge := promotion.Edge{
		SrcRegistry: registry.Context{Name: "gcr.io/staging/b", Src: true},
		SrcImageTag: promotion.ImageTag{Name: nestedName, Tag: "v1"},
		Digest:      digest,
		DstRegistry: registry.Context{Name: "gcr.io/prod"},
	}

	for _, tc := range []struct {
		name   string
		levels []provenance.ImageLevel
		want   []string
	}{
		{
			name:   "different levels for the image are kept apart",
			levels: []provenance.ImageLevel{{Images: []string{parentName}, Level: 3}, {Images: []string{nestedName}, Level: 2}},
			want:   []string{parentName, nestedName},
		},
		{
			name:   "the same level for the image counts once",
			levels: []provenance.ImageLevel{{Images: []string{parentName, nestedName}, Level: 3}},
			want:   []string{parentName},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := &provenance.Policy{
				Mode:     provenance.PolicyModeRequire,
				Signers:  []string{"sigstore::https://accounts.google.com::a@example.com"},
				Builders: []provenance.Builder{{ID: "https://builder.example.com", Level: 3}},
				Sources:  []string{"github.com/example/a"},
				Levels:   tc.levels,
			}

			mfests := []schema.Manifest{
				{
					Registries: []registry.Context{{Name: "gcr.io/staging", Src: true}},
					Provenance: policy,
					Filepath:   "a/promoter-manifest.yaml",
				},
				{
					Registries: []registry.Context{edge.SrcRegistry},
					Provenance: policy,
					Filepath:   "b/promoter-manifest.yaml",
				},
			}

			refPolicies, err := sourcePolicies(mfests, map[promotion.Edge]any{edge: nil})
			require.NoError(t, err)

			images := make([]string, 0, len(refPolicies[edge.SrcReference()]))
			for _, applied := range refPolicies[edge.SrcReference()] {
				images = append(images, applied.Image)
			}

			require.Equal(t, tc.want, images)
		})
	}
}
