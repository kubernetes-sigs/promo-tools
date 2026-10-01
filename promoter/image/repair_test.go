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
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// repairManifests returns a manifest promoting two tags of the test image
// to production, with the given provenance policy.
func repairManifests(policy *provenance.Policy) []schema.Manifest {
	src := registry.Context{Name: testEdge().SrcRegistry.Name, Src: true}

	return []schema.Manifest{{
		Registries:  []registry.Context{src, {Name: image.Registry("gcr.io/production")}},
		SrcRegistry: &src,
		Provenance:  policy,
		Filepath:    "manifests/test/promoter-manifest.yaml",
		Images: []registry.Image{{
			Name: testEdge().SrcImageTag.Name,
			Dmap: registry.DigestTags{testEdge().Digest: {"v1", "v1.0"}},
		}},
	}}
}

// repairTest is a promoter whose implementation reports every candidate
// as missing attestations.
type repairTest struct {
	sut        *imagepromoter.Promoter
	mock       *imagefakes.FakePromoterImplementation
	discoverer *provenancefakes.FakeDiscoverer
	edges      map[promotion.Edge]any
}

func newRepairTest(t *testing.T, policy *provenance.Policy) *repairTest {
	t.Helper()

	mfests := repairManifests(policy)

	edges, err := promotion.ToEdges(mfests)
	require.NoError(t, err)
	require.Len(t, edges, 2)

	rt := &repairTest{
		sut:        &imagepromoter.Promoter{},
		mock:       &imagefakes.FakePromoterImplementation{},
		discoverer: &provenancefakes.FakeDiscoverer{},
		edges:      edges,
	}

	rt.mock.ParseManifestsReturns(mfests, nil)
	rt.mock.FindAttestationRepairsStub = func(
		_ context.Context, _ *options.Options, candidates map[promotion.Edge]any,
	) (map[promotion.Edge]any, error) {
		return candidates, nil
	}
	rt.discoverer.DiscoverStub = func(_ context.Context, ref string) (*provenance.Discovery, error) {
		return &provenance.Discovery{Reference: ref}, nil
	}

	rt.sut.SetImplementation(rt.mock)
	rt.sut.SetDiscoverer(rt.discoverer)
	rt.sut.SetProvenanceVerifier(&fakeVerifier{result: &provenance.Result{Verified: true}})

	return rt
}

func TestPromoteImagesRepair(t *testing.T) {
	// The promoted image no longer satisfies the require policy, which
	// blocks nothing: it is promoted already, and its summary records it.
	rt := newRepairTest(t, testProvenancePolicy(provenance.PolicyModeRequire))

	opts := &options.Options{Confirm: true, SignImages: true}
	require.NoError(t, rt.sut.PromoteImages(context.Background(), opts))

	require.Equal(t, 1, rt.mock.FindAttestationRepairsCallCount())
	_, _, candidates := rt.mock.FindAttestationRepairsArgsForCall(0)
	require.Equal(t, rt.edges, candidates)

	edge := testEdge()
	ref := edge.SrcReference()

	// Discovered once for both tags.
	require.Equal(t, 1, rt.discoverer.DiscoverCallCount())

	// The attest phase of this run had nothing, the repair carries and
	// writes for the promoted image.
	require.Equal(t, 2, rt.mock.CarryAttestationsCallCount())
	_, _, carryEdges, outcomes := rt.mock.CarryAttestationsArgsForCall(1)
	require.Equal(t, rt.edges, carryEdges)
	require.Contains(t, outcomes, ref)
	require.Len(t, outcomes[ref].Results, 1)
	require.False(t, outcomes[ref].Results[0].Satisfied)

	require.Equal(t, 2, rt.mock.WriteVerificationSummariesCallCount())
	_, _, mfests, summaryEdges, discoveries, summaryOutcomes := rt.mock.WriteVerificationSummariesArgsForCall(1)
	require.Len(t, mfests, 1)
	require.Equal(t, rt.edges, summaryEdges)
	require.Contains(t, discoveries, ref)
	require.Equal(t, outcomes, summaryOutcomes)

	// The results are reported with those of the run.
	require.Contains(t, rt.sut.Discoveries(), ref)
	require.Contains(t, rt.sut.Provenance(), ref)

	// The promotion records are left to sigcheck.
	require.Equal(t, 1, rt.mock.WriteProvenanceAttestationsCallCount())
}

func TestPromoteImagesRepairCandidates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		policy    *provenance.Policy
		promoting bool
		confirm   bool
		noSign    bool
		want      int
	}{
		{name: "promoted images with a policy", policy: testProvenancePolicy(provenance.PolicyModeWarn), confirm: true, want: 2},
		{name: "images promoted in this run", policy: testProvenancePolicy(provenance.PolicyModeWarn), promoting: true, confirm: true, want: 1},
		{name: "no policy", confirm: true},
		{name: "policy off", policy: &provenance.Policy{Mode: provenance.PolicyModeOff}, confirm: true},
		{name: "dry run", policy: testProvenancePolicy(provenance.PolicyModeWarn)},
		{name: "without signing", policy: testProvenancePolicy(provenance.PolicyModeWarn), confirm: true, noSign: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newRepairTest(t, tc.policy)

			// One tag is promoted in this run, its attest phase covers it.
			var promoting promotion.Edge

			if tc.promoting {
				for edge := range rt.edges {
					promoting = edge

					break
				}

				rt.mock.GetPromotionEdgesReturns(map[promotion.Edge]any{promoting: nil}, nil)
			}

			require.NoError(t, rt.sut.PromoteImages(context.Background(),
				&options.Options{Confirm: tc.confirm, SignImages: !tc.noSign}))

			if tc.want == 0 {
				require.Equal(t, 0, rt.mock.FindAttestationRepairsCallCount())

				return
			}

			require.Equal(t, 1, rt.mock.FindAttestationRepairsCallCount())
			_, _, candidates := rt.mock.FindAttestationRepairsArgsForCall(0)
			require.Len(t, candidates, tc.want)
			require.NotContains(t, candidates, promoting)
		})
	}
}

func TestPromoteImagesRepairPromotedInRun(t *testing.T) {
	// One tag of the image is promoted in this run, the other one is
	// repaired: the image is discovered once, and the provenance phase
	// result that decided the promotion is kept.
	rt := newRepairTest(t, testProvenancePolicy(provenance.PolicyModeWarn))

	var promoting promotion.Edge
	for edge := range rt.edges {
		promoting = edge

		break
	}

	rt.mock.GetPromotionEdgesReturns(map[promotion.Edge]any{promoting: nil}, nil)

	ref := promoting.SrcReference()

	// The outcomes are recorded when they are carried, the maps of the
	// calls are those of the promoter.
	var (
		carried     []*provenance.ImageProvenance
		repairEdges map[promotion.Edge]any
	)

	rt.mock.CarryAttestationsStub = func(
		_ context.Context, _ *options.Options, edges map[promotion.Edge]any, outcomes map[string]*provenance.ImageProvenance,
	) error {
		carried = append(carried, outcomes[ref])
		repairEdges = edges

		return nil
	}

	require.NoError(t, rt.sut.PromoteImages(context.Background(), &options.Options{Confirm: true, SignImages: true}))

	require.Equal(t, 1, rt.discoverer.DiscoverCallCount())
	require.Len(t, carried, 2)
	require.NotNil(t, carried[0])
	require.NotNil(t, carried[1])
	require.NotSame(t, carried[0], carried[1])
	require.Same(t, carried[0], rt.sut.Provenance()[ref])
	require.NotContains(t, repairEdges, promoting)
}

func TestPromoteImagesRepairNothingMissing(t *testing.T) {
	rt := newRepairTest(t, testProvenancePolicy(provenance.PolicyModeWarn))
	rt.mock.FindAttestationRepairsStub = nil
	rt.mock.FindAttestationRepairsReturns(nil, nil)

	require.NoError(t, rt.sut.PromoteImages(context.Background(), &options.Options{Confirm: true, SignImages: true}))

	// Nothing is discovered or written for complete images.
	require.Equal(t, 0, rt.discoverer.DiscoverCallCount())
	require.Equal(t, 1, rt.mock.CarryAttestationsCallCount())
	require.Equal(t, 1, rt.mock.WriteVerificationSummariesCallCount())
}

func TestPromoteImagesRepairErrors(t *testing.T) {
	// A failed check only warns, the images found are repaired anyway, and
	// a failed repair does not fail the promotion.
	rt := newRepairTest(t, testProvenancePolicy(provenance.PolicyModeWarn))
	found := rt.mock.FindAttestationRepairsStub
	rt.mock.FindAttestationRepairsStub = func(
		ctx context.Context, opts *options.Options, candidates map[promotion.Edge]any,
	) (map[promotion.Edge]any, error) {
		repairs, _ := found(ctx, opts, candidates) //nolint:errcheck // the stub returns none

		return repairs, errors.New("registry unavailable")
	}
	rt.mock.CarryAttestationsReturnsOnCall(1, errors.New("copy failed"))

	require.NoError(t, rt.sut.PromoteImages(context.Background(), &options.Options{Confirm: true, SignImages: true}))
	require.Equal(t, 2, rt.mock.CarryAttestationsCallCount())
	require.Equal(t, 2, rt.mock.WriteVerificationSummariesCallCount())
}

func TestPromoteImagesRepairWithoutResult(t *testing.T) {
	for name, discover := range map[string]func(context.Context, string) (*provenance.Discovery, error){
		// An image whose attestations can't be discovered, for example
		// because it is gone from staging, gets nothing written.
		"not discovered": func(context.Context, string) (*provenance.Discovery, error) {
			return nil, errors.New("not found")
		},
		// Neither does one whose evaluation fails.
		"evaluation failed": func(context.Context, string) (*provenance.Discovery, error) {
			return &provenance.Discovery{Reference: "gcr.io/other/image@" + string(testEdge().Digest)}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			rt := newRepairTest(t, testProvenancePolicy(provenance.PolicyModeRequire))
			rt.discoverer.DiscoverStub = discover

			require.NoError(t, rt.sut.PromoteImages(context.Background(), &options.Options{Confirm: true, SignImages: true}))

			require.Equal(t, 2, rt.mock.CarryAttestationsCallCount())
			_, _, carryEdges, outcomes := rt.mock.CarryAttestationsArgsForCall(1)
			require.Equal(t, rt.edges, carryEdges)
			require.Empty(t, outcomes)

			_, _, _, _, _, summaryOutcomes := rt.mock.WriteVerificationSummariesArgsForCall(1)
			require.Empty(t, summaryOutcomes)
		})
	}
}
