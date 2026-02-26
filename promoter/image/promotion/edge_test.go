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

package promotion

import (
	"testing"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

var (
	testSrcRC = registry.Context{
		Name:           "gcr.io/staging",
		ServiceAccount: "sa@staging.iam.gserviceaccount.com",
		Src:            true,
	}
	testDstRC1 = registry.Context{
		Name:           "us.gcr.io/prod",
		ServiceAccount: "sa@prod.iam.gserviceaccount.com",
	}
	testDstRC2 = registry.Context{
		Name:           "eu.gcr.io/prod",
		ServiceAccount: "sa@prod.iam.gserviceaccount.com",
	}
	testDigest1 = image.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	testDigest2 = image.Digest("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
)

func TestEdgeSrcReference(t *testing.T) {
	e := Edge{
		SrcRegistry: testSrcRC,
		SrcImageTag: ImageTag{Name: "foo", Tag: "v1"},
		Digest:      testDigest1,
	}
	require.Equal(t, "gcr.io/staging/foo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", e.SrcReference())

	// Empty fields return empty string.
	require.Empty(t, (&Edge{}).SrcReference())
}

func TestEdgeDstReference(t *testing.T) {
	e := Edge{
		DstRegistry: testDstRC1,
		DstImageTag: ImageTag{Name: "foo", Tag: "v1"},
		Digest:      testDigest1,
	}
	require.Equal(t, "us.gcr.io/prod/foo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", e.DstReference())

	require.Empty(t, (&Edge{}).DstReference())
}

func TestToFQIN(t *testing.T) {
	require.Equal(t, "gcr.io/staging/foo@sha256:abc", ToFQIN("gcr.io/staging", "foo", "sha256:abc"))
}

func TestToPQIN(t *testing.T) {
	require.Equal(t, "gcr.io/staging/foo:v1", ToPQIN("gcr.io/staging", "foo", "v1"))
}

func testManifest() schema.Manifest {
	return schema.Manifest{
		SrcRegistry: &testSrcRC,
		Registries: []registry.Context{
			testSrcRC,
			testDstRC1,
			testDstRC2,
		},
		Images: []registry.Image{
			{
				Name: "foo",
				Dmap: registry.DigestTags{
					testDigest1: {"v1", "latest"},
				},
			},
		},
	}
}

func TestToEdges(t *testing.T) {
	edges, err := ToEdges([]schema.Manifest{testManifest()})
	require.NoError(t, err)

	// 1 image × 2 tags × 2 dst registries = 4 edges
	require.Len(t, edges, 4)

	// Verify source registry is never a destination.
	for edge := range edges {
		require.NotEqual(t, testSrcRC.Name, edge.DstRegistry.Name)
		require.Equal(t, testSrcRC.Name, edge.SrcRegistry.Name)
	}
}

func TestToEdgesTagless(t *testing.T) {
	mfest := schema.Manifest{
		SrcRegistry: &testSrcRC,
		Registries: []registry.Context{
			testSrcRC,
			testDstRC1,
		},
		Images: []registry.Image{
			{
				Name: "bar",
				Dmap: registry.DigestTags{
					testDigest1: {}, // tagless
				},
			},
		},
	}
	edges, err := ToEdges([]schema.Manifest{mfest})
	require.NoError(t, err)
	require.Len(t, edges, 1)

	for edge := range edges {
		require.Equal(t, image.Tag(""), edge.DstImageTag.Tag)
	}
}

func TestCheckOverlappingEdgesClean(t *testing.T) {
	edges := map[Edge]any{
		{
			SrcRegistry: testSrcRC,
			SrcImageTag: ImageTag{Name: "foo", Tag: "v1"},
			Digest:      testDigest1,
			DstRegistry: testDstRC1,
			DstImageTag: ImageTag{Name: "foo", Tag: "v1"},
		}: nil,
	}
	checked, err := CheckOverlappingEdges(edges)
	require.NoError(t, err)
	require.Len(t, checked, 1)
}

func TestCheckOverlappingEdgesConflict(t *testing.T) {
	// Two different digests targeting the same destination tag.
	edges := map[Edge]any{
		{
			SrcRegistry: testSrcRC,
			SrcImageTag: ImageTag{Name: "foo", Tag: "v1"},
			Digest:      testDigest1,
			DstRegistry: testDstRC1,
			DstImageTag: ImageTag{Name: "foo", Tag: "v1"},
		}: nil,
		{
			SrcRegistry: testSrcRC,
			SrcImageTag: ImageTag{Name: "foo", Tag: "v1"},
			Digest:      testDigest2,
			DstRegistry: testDstRC1,
			DstImageTag: ImageTag{Name: "foo", Tag: "v1"},
		}: nil,
	}
	_, err := CheckOverlappingEdges(edges)
	require.Error(t, err)
	require.Contains(t, err.Error(), "overlapping")
}

func TestGetPromotionCandidates(t *testing.T) {
	edges := map[Edge]any{
		{
			SrcRegistry: testSrcRC,
			SrcImageTag: ImageTag{Name: "foo", Tag: "v1"},
			Digest:      testDigest1,
			DstRegistry: testDstRC1,
			DstImageTag: ImageTag{Name: "foo", Tag: "v1"},
		}: nil,
		{
			SrcRegistry: testSrcRC,
			SrcImageTag: ImageTag{Name: "bar", Tag: "v2"},
			Digest:      testDigest2,
			DstRegistry: testDstRC1,
			DstImageTag: ImageTag{Name: "bar", Tag: "v2"},
		}: nil,
	}

	// Inventory where "foo:v1" is already promoted but "bar:v2" is not.
	inv := map[image.Registry]registry.RegInvImage{
		testSrcRC.Name: {
			"foo": registry.DigestTags{testDigest1: {"v1"}},
			"bar": registry.DigestTags{testDigest2: {"v2"}},
		},
		testDstRC1.Name: {
			"foo": registry.DigestTags{testDigest1: {"v1"}}, // already promoted
		},
	}

	candidates, clean := GetPromotionCandidates(edges, inv)
	require.True(t, clean)
	require.Len(t, candidates, 1)

	for edge := range candidates {
		require.Equal(t, image.Name("bar"), edge.DstImageTag.Name)
	}
}

func TestGetPromotionCandidatesTagMove(t *testing.T) {
	edge := Edge{
		SrcRegistry: testSrcRC,
		SrcImageTag: ImageTag{Name: "foo", Tag: "v1"},
		Digest:      testDigest1,
		DstRegistry: testDstRC1,
		DstImageTag: ImageTag{Name: "foo", Tag: "v1"},
	}
	edges := map[Edge]any{edge: nil}

	// Tag "v1" in dst points to a different digest — tag move.
	inv := map[image.Registry]registry.RegInvImage{
		testSrcRC.Name: {
			"foo": registry.DigestTags{testDigest1: {"v1"}},
		},
		testDstRC1.Name: {
			"foo": registry.DigestTags{testDigest2: {"v1"}},
		},
	}

	candidates, clean := GetPromotionCandidates(edges, inv)
	require.False(t, clean)
	require.Empty(t, candidates)
}

func TestVertexProps(t *testing.T) {
	edge := Edge{
		SrcRegistry: testSrcRC,
		SrcImageTag: ImageTag{Name: "foo", Tag: "v1"},
		Digest:      testDigest1,
		DstRegistry: testDstRC1,
		DstImageTag: ImageTag{Name: "foo", Tag: "v1"},
	}

	inv := map[image.Registry]registry.RegInvImage{
		testSrcRC.Name: {
			"foo": registry.DigestTags{testDigest1: {"v1"}},
		},
		testDstRC1.Name: {
			"foo": registry.DigestTags{testDigest1: {"v1"}},
		},
	}

	srcP, dstP := edge.VertexProps(inv)
	require.True(t, srcP.DigestExists)
	require.True(t, srcP.PqinDigestMatch)
	require.True(t, dstP.DigestExists)
	require.True(t, dstP.PqinDigestMatch)
}

func TestVertexPropsNotPromoted(t *testing.T) {
	edge := Edge{
		SrcRegistry: testSrcRC,
		SrcImageTag: ImageTag{Name: "foo", Tag: "v1"},
		Digest:      testDigest1,
		DstRegistry: testDstRC1,
		DstImageTag: ImageTag{Name: "foo", Tag: "v1"},
	}

	inv := map[image.Registry]registry.RegInvImage{
		testSrcRC.Name: {
			"foo": registry.DigestTags{testDigest1: {"v1"}},
		},
		// Destination registry is empty.
	}

	srcP, dstP := edge.VertexProps(inv)
	require.True(t, srcP.DigestExists)
	require.False(t, dstP.DigestExists)
	require.False(t, dstP.PqinExists)
}

func TestEdgesToRegInvImage(t *testing.T) {
	edges := map[Edge]any{
		{
			DstRegistry: testDstRC1,
			DstImageTag: ImageTag{Name: "foo", Tag: "v1"},
			Digest:      testDigest1,
		}: nil,
		{
			DstRegistry: testDstRC1,
			DstImageTag: ImageTag{Name: "foo", Tag: "v2"},
			Digest:      testDigest1,
		}: nil,
		{
			DstRegistry: testDstRC2,
			DstImageTag: ImageTag{Name: "bar", Tag: "latest"},
			Digest:      testDigest2,
		}: nil,
	}

	rii := EdgesToRegInvImage(edges, "us.gcr.io/prod")
	require.Len(t, rii, 1) // only "foo" from us.gcr.io/prod
	require.Contains(t, rii, image.Name("foo"))
	require.Len(t, rii["foo"][testDigest1], 2)
}

func TestFilterByTag(t *testing.T) {
	rii := registry.RegInvImage{
		"foo": registry.DigestTags{
			testDigest1: {"v1", "latest"},
			testDigest2: {"v2"},
		},
	}

	filtered := FilterByTag(rii, "v1")
	require.Len(t, filtered, 1)
	require.Contains(t, filtered, image.Name("foo"))
	require.Len(t, filtered["foo"], 1)
	require.Contains(t, filtered["foo"], testDigest1)
}

func TestFilterByTagNoMatch(t *testing.T) {
	rii := registry.RegInvImage{
		"foo": registry.DigestTags{
			testDigest1: {"v1"},
		},
	}

	filtered := FilterByTag(rii, "nonexistent")
	require.Empty(t, filtered)
}

func TestGetRegistriesToRead(t *testing.T) {
	edges := map[Edge]any{
		{
			SrcRegistry: testSrcRC,
			SrcImageTag: ImageTag{Name: "foo"},
			DstRegistry: testDstRC1,
			DstImageTag: ImageTag{Name: "foo"},
		}: nil,
	}

	rcs := GetRegistriesToRead(edges)
	require.Len(t, rcs, 2)

	names := make(map[image.Registry]bool)
	for _, rc := range rcs {
		names[rc.Name] = true
	}

	require.True(t, names["gcr.io/staging/foo"])
	require.True(t, names["us.gcr.io/prod/foo"])
}

func TestGetBaseRegistries(t *testing.T) {
	edges := map[Edge]any{
		{
			SrcRegistry: testSrcRC,
			SrcImageTag: ImageTag{Name: "foo"},
			DstRegistry: testDstRC1,
			DstImageTag: ImageTag{Name: "foo"},
		}: nil,
		{
			SrcRegistry: testSrcRC,
			SrcImageTag: ImageTag{Name: "bar"},
			DstRegistry: testDstRC2,
			DstImageTag: ImageTag{Name: "bar"},
		}: nil,
	}

	rcs := GetBaseRegistries(edges)
	require.Len(t, rcs, 3)

	names := make(map[image.Registry]bool)
	for _, rc := range rcs {
		names[rc.Name] = true
	}
	// Base registries should NOT have image names appended.
	require.True(t, names["gcr.io/staging"])
	require.True(t, names["us.gcr.io/prod"])
	require.True(t, names["eu.gcr.io/prod"])
}

func TestGetPromotionCandidatesWithBaseRegistries(t *testing.T) {
	// This test verifies the fix for the inventory key mismatch bug.
	// When ReadRegistries uses base registries for splitByKnownRegistries,
	// the inventory is keyed correctly and digests are found.
	srcReg := registry.Context{
		Name: "gcr.io/k8s-staging-foo",
		Src:  true,
	}
	dstReg := registry.Context{
		Name: "us-docker.pkg.dev/k8s-artifacts-prod/images/foo",
	}

	edges := map[Edge]any{
		{
			SrcRegistry: srcReg,
			SrcImageTag: ImageTag{Name: "myimage", Tag: "v1.0"},
			DstRegistry: dstReg,
			DstImageTag: ImageTag{Name: "myimage", Tag: "v1.0"},
			Digest:      testDigest1,
		}: nil,
	}

	// Inventory keyed by BASE registry name with image name as sub-key
	// (this is the correct keying produced when base registries are used).
	// Both src and dst have the digest+tag → already promoted.
	inv := map[image.Registry]registry.RegInvImage{
		"gcr.io/k8s-staging-foo": {
			"myimage": registry.DigestTags{
				testDigest1: {"v1.0"},
			},
		},
		"us-docker.pkg.dev/k8s-artifacts-prod/images/foo": {
			"myimage": registry.DigestTags{
				testDigest1: {"v1.0"},
			},
		},
	}

	candidates, clean := GetPromotionCandidates(edges, inv)
	require.True(t, clean)
	// Already promoted, so no candidates.
	require.Empty(t, candidates)

	// Now test with a digest that needs promotion (exists in src, not in dst).
	inv = map[image.Registry]registry.RegInvImage{
		"gcr.io/k8s-staging-foo": {
			"myimage": registry.DigestTags{
				testDigest1: {"v1.0"},
			},
		},
		"us-docker.pkg.dev/k8s-artifacts-prod/images/foo": {
			"myimage": registry.DigestTags{},
		},
	}

	candidates, clean = GetPromotionCandidates(edges, inv)
	require.True(t, clean)
	require.Len(t, candidates, 1)

	// Verify that with WRONG keying (full-path keys as produced by the
	// old buggy code), digests would NOT be found and candidates would
	// be empty (all _LOST_).
	invBadKeys := map[image.Registry]registry.RegInvImage{
		"gcr.io/k8s-staging-foo/myimage": {
			"": registry.DigestTags{
				testDigest1: {"v1.0"},
			},
		},
	}

	candidates, clean = GetPromotionCandidates(edges, invBadKeys)
	require.True(t, clean)
	// With wrong keys, nothing is found → no candidates (all _LOST_).
	require.Empty(t, candidates)
}
