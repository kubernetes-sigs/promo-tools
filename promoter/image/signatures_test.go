/*
Copyright 2026 The Kubernetes Authors.

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

package imagepromoter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	imagepromoter "sigs.k8s.io/promo-tools/v4/promoter/image"
	imagefakes "sigs.k8s.io/promo-tools/v4/promoter/image/imagefakes"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance/provenancefakes"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// stagingEdge returns an edge promoting the staging image to a tag.
func stagingEdge(imageName, tag string) promotion.Edge {
	return promotion.Edge{
		SrcRegistry: registry.Context{Name: "gcr.io/staging", Src: true},
		SrcImageTag: promotion.ImageTag{Name: image.Name(imageName)},
		Digest:      "sha256:abc",
		DstRegistry: registry.Context{Name: "gcr.io/production"},
		DstImageTag: promotion.ImageTag{Name: image.Name(imageName), Tag: image.Tag(tag)},
	}
}

func TestPromoteImagesStagingSignatures(t *testing.T) {
	sut := imagepromoter.Promoter{}
	sut.SetProvenanceVerifier(&fakeVerifier{
		result: &provenance.Result{Verified: true},
	})

	edges := map[promotion.Edge]any{
		stagingEdge("found", "v1.0"):  nil,
		stagingEdge("found", "v1.1"):  nil,
		stagingEdge("failed", "v1.0"): nil,
	}

	discoverer := &provenancefakes.FakeDiscoverer{}
	discoverer.DiscoverStub = func(_ context.Context, ref string) (*provenance.Discovery, error) {
		if ref == "gcr.io/staging/failed@sha256:abc" {
			return nil, errors.New("discovery failed")
		}

		return &provenance.Discovery{Reference: ref}, nil
	}
	sut.SetDiscoverer(discoverer)

	results := promotion.StagingSignatures{
		"gcr.io/staging/found@sha256:abc": {Status: provenance.SignatureVerified},
	}

	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(nonEmptyManifests(), nil)
	mock.GetPromotionEdgesReturns(edges, nil)
	mock.ValidateStagingSignaturesReturns(results, nil)
	sut.SetImplementation(&mock)

	require.NoError(t, sut.PromoteImages(context.Background(), &options.Options{}))

	// Each staging image is discovered once, whatever its number of edges.
	require.Equal(t, 2, discoverer.DiscoverCallCount())

	require.Equal(t, 1, mock.ValidateStagingSignaturesCallCount())
	_, _, gotEdges, discoveries, outcomes := mock.ValidateStagingSignaturesArgsForCall(0)
	require.Equal(t, edges, gotEdges)
	require.Len(t, discoveries, 1, "a failed discovery is left out")
	require.Contains(t, discoveries, "gcr.io/staging/found@sha256:abc")
	require.Contains(t, outcomes, "gcr.io/staging/found@sha256:abc",
		"the provenance outcomes are passed on")

	require.Equal(t, results, sut.StagingSignatures())
}

func TestPromoteImagesWithoutDiscoverer(t *testing.T) {
	sut := imagepromoter.Promoter{}
	sut.SetProvenanceVerifier(&fakeVerifier{
		result: &provenance.Result{Verified: true},
	})

	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(nonEmptyManifests(), nil)
	mock.GetPromotionEdgesReturns(map[promotion.Edge]any{stagingEdge("image", "v1.0"): nil}, nil)
	sut.SetImplementation(&mock)

	require.NoError(t, sut.PromoteImages(context.Background(), &options.Options{}))

	_, gotOpts, _, discoveries, outcomes := mock.ValidateStagingSignaturesArgsForCall(0)
	require.NotNil(t, gotOpts)
	require.Nil(t, discoveries, "bundles are not checked without a discoverer")
	require.Len(t, outcomes, 1, "the provenance outcomes are passed on anyway")
}

func TestPromoteImagesInvalidStagingSignatures(t *testing.T) {
	sut := imagepromoter.Promoter{}
	sut.SetProvenanceVerifier(&fakeVerifier{
		result: &provenance.Result{Verified: true},
	})

	results := promotion.StagingSignatures{
		"gcr.io/staging/image@sha256:abc": {Status: provenance.SignatureFailed},
	}

	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(nonEmptyManifests(), nil)
	mock.GetPromotionEdgesReturns(map[promotion.Edge]any{stagingEdge("image", "v1.0"): nil}, nil)
	mock.ValidateStagingSignaturesReturns(results, errors.New("staging signatures failed to verify"))
	sut.SetImplementation(&mock)

	require.Error(t, sut.PromoteImages(context.Background(), &options.Options{Confirm: true}))
	require.Equal(t, 0, mock.PromoteImagesCallCount())

	// The results describe the failed run.
	require.Equal(t, results, sut.StagingSignatures())

	// A new run starts without them.
	mock.GetPromotionEdgesReturns(nil, errors.New("no edges"))
	require.Error(t, sut.PromoteImages(context.Background(), &options.Options{Confirm: true}))
	require.Nil(t, sut.StagingSignatures())
}
