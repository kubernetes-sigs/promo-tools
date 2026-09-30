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

package imagepromoter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/release-utils/command"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	reg "sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// Verification results of the summaries.
const (
	summaryPassed = "PASSED"
	summaryFailed = "FAILED"
)

// recordingSigner records the statements it signs and returns a fixed
// bundle.
type recordingSigner struct {
	mu         sync.Mutex
	statements [][]byte
}

func (r *recordingSigner) SignStatement(statement []byte) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.statements = append(r.statements, statement)

	return testBundle(statement)
}

// testBundle returns a sigstore bundle holding the statement. It is not
// signed properly.
func testBundle(statement []byte) ([]byte, error) {
	return fmt.Appendf(nil,
		`{"mediaType": %q, "verificationMaterial": {"publicKey": {"hint": "test"}}, `+
			`"dsseEnvelope": {"payloadType": "application/vnd.in-toto+json", "payload": %q, "signatures": [{"sig": "c2ln"}]}}`,
		provenance.BundleArtifactType, base64.StdEncoding.EncodeToString(statement),
	), nil
}

// gitManifest writes a promoter manifest into a git repository and returns
// its path.
func gitManifest(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	path := filepath.Join(repo, "promoter-manifest.yaml")
	require.NoError(t, os.WriteFile(path, []byte("registries: []\n"), 0o600))

	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "https://github.com/kubernetes/k8s.io"},
		{"add", "."},
		{"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "manifest"},
	} {
		require.NoError(t, command.NewWithWorkDir(repo, "git", args...).RunSilentSuccess())
	}

	return path
}

// summaryManifests returns the promoter manifest of the test image.
func (st *summaryTest) summaryManifests(t *testing.T, policy *provenance.Policy) []schema.Manifest {
	t.Helper()

	return []schema.Manifest{{
		SrcRegistry: &st.edge.SrcRegistry,
		Filepath:    gitManifest(t),
		Provenance:  policy,
		Images: []reg.Image{{
			Name: testImageApp,
			Dmap: reg.DigestTags{image.Digest(st.digest): {testTagV1}},
		}},
	}}
}

// summaryPredicate is the part of a verification summary the tests check.
type summaryPredicate struct {
	Subject []struct {
		Name string `json:"name"`
	} `json:"subject"`
	PredicateType string `json:"predicateType"`
	Predicate     struct {
		Verifier struct {
			ID string `json:"id"`
		} `json:"verifier"`
		ResourceURI string `json:"resourceUri"`
		Policy      struct {
			URI string `json:"uri"`
		} `json:"policy"`
		VerificationResult string   `json:"verificationResult"`
		VerifiedLevels     []string `json:"verifiedLevels"`
	} `json:"predicate"`
}

// statement returns the only statement the test signed.
func (st *summaryTest) statement(t *testing.T) *summaryPredicate {
	t.Helper()

	require.Len(t, st.signer.statements, 1)

	stmt := &summaryPredicate{}
	require.NoError(t, json.Unmarshal(st.signer.statements[0], stmt))

	return stmt
}

// summaryTest is a promoted image with the edges and results a
// verification summary is written from.
type summaryTest struct {
	di       *DefaultPromoterImplementation
	signer   *recordingSigner
	edges    map[promotion.Edge]any
	edge     promotion.Edge
	digest   string
	outcomes map[string]*provenance.ImageProvenance
	opts     *options.Options
}

// newSummaryTest pushes an image to production and returns the edges of
// three tags and one tagless edge of its digest.
func newSummaryTest(t *testing.T) *summaryTest {
	t.Helper()

	host, di := newTLSTestRegistry(t)

	srcRegistry := image.Registry(host + "/staging")
	dstRegistry := image.Registry(host + "/production")

	digest := pushTestImage(t, di, fmt.Sprintf("%s/app:v1.0", dstRegistry))

	mkEdge := func(tag image.Tag) promotion.Edge {
		return promotion.Edge{
			SrcRegistry: reg.Context{Name: srcRegistry, Src: true},
			SrcImageTag: promotion.ImageTag{Name: testImageApp, Tag: tag},
			Digest:      image.Digest(digest),
			DstRegistry: reg.Context{Name: dstRegistry},
			DstImageTag: promotion.ImageTag{Name: testImageApp, Tag: tag},
		}
	}

	edge := mkEdge(testTagV1)
	signer := &recordingSigner{}
	di.attSigner = signer

	return &summaryTest{
		di:     di,
		signer: signer,
		edges: map[promotion.Edge]any{
			edge:           nil,
			mkEdge("v1"):   nil,
			mkEdge(""):     nil,
			mkEdge("v1.1"): nil,
		},
		edge:   edge,
		digest: digest,
		outcomes: map[string]*provenance.ImageProvenance{
			edge.SrcReference(): {},
		},
		opts: &options.Options{
			SignImages:            true,
			VerificationSummaries: true,
			MaxSignatureOps:       10,
		},
	}
}

// referrers returns the number of verification summary referrers of the
// promoted digest.
func (st *summaryTest) referrers(t *testing.T) int {
	t.Helper()

	digestRef, err := name.NewDigest(fmt.Sprintf("%s/%s@%s", st.edge.DstRegistry.Name, testImageApp, st.digest))
	require.NoError(t, err)

	idx, err := ociremote.Referrers(
		digestRef, "", ociremote.WithRemoteOptions(remote.WithTransport(st.di.getTransport())),
	)
	require.NoError(t, err)

	count := 0

	for _, desc := range idx.Manifests {
		if desc.Annotations[bundlePredicateTypeAnnotation] == provenance.SummaryPredicateType {
			count++
		}
	}

	return count
}

func TestWriteVerificationSummaries(t *testing.T) {
	t.Parallel()

	st := newSummaryTest(t)
	mfests := st.summaryManifests(t, nil)

	// Written once, whatever the number of tags and runs.
	for range 2 {
		require.NoError(t, st.di.WriteVerificationSummaries(
			context.Background(), st.opts, mfests, st.edges, nil, st.outcomes,
		))
	}

	require.Equal(t, 1, st.referrers(t))

	stmt := st.statement(t)
	require.Equal(t, provenance.SummaryPredicateType, stmt.PredicateType)
	require.Equal(t, targetIdentity(&st.edge), stmt.Subject[0].Name)
	require.Equal(t, targetIdentity(&st.edge)+"@"+st.digest, stmt.Predicate.ResourceURI)
	require.Equal(t, "git+https://github.com/kubernetes/k8s.io#promoter-manifest.yaml", stmt.Predicate.Policy.URI)
	require.Equal(t, summaryPassed, stmt.Predicate.VerificationResult)
}

func TestWriteVerificationSummariesOtherSummary(t *testing.T) {
	t.Parallel()

	st := newSummaryTest(t)

	// A summary of another verifier, for example one carried from staging,
	// does not count as the promoter's.
	other := fmt.Appendf(nil,
		`{"_type": "https://in-toto.io/Statement/v1", "subject": [{"name": %q, "digest": {"sha256": %q}}], `+
			`"predicateType": %q, "predicate": {"verifier": {"id": "https://example.com/verifier"}}}`,
		testImageApp, st.digest[7:], provenance.SummaryPredicateType,
	)

	bundleJSON, err := testBundle(other)
	require.NoError(t, err)

	digestRef, err := name.NewDigest(fmt.Sprintf("%s/%s@%s", st.edge.DstRegistry.Name, testImageApp, st.digest))
	require.NoError(t, err)
	require.NoError(t, ociremote.WriteAttestationNewBundleFormat(digestRef, bundleJSON, provenance.SummaryPredicateType,
		ociremote.WithRemoteOptions(st.di.remoteOptions()...)))

	require.NoError(t, st.di.WriteVerificationSummaries(
		context.Background(), st.opts, st.summaryManifests(t, nil), st.edges, nil, st.outcomes,
	))
	require.Equal(t, provenance.SummaryVerifierID, st.statement(t).Predicate.Verifier.ID)
	require.Equal(t, 2, st.referrers(t))
}

func TestWriteVerificationSummariesWithoutCommit(t *testing.T) {
	t.Parallel()

	st := newSummaryTest(t)

	// A manifest that is not in a repository was not reviewed.
	path := filepath.Join(t.TempDir(), "promoter-manifest.yaml")
	require.NoError(t, os.WriteFile(path, []byte("registries: []\n"), 0o600))

	mfests := st.summaryManifests(t, nil)
	mfests[0].Filepath = path

	require.NoError(t, st.di.WriteVerificationSummaries(
		context.Background(), st.opts, mfests, st.edges, nil, st.outcomes,
	))
	require.Empty(t, st.signer.statements)
	require.Equal(t, 0, st.referrers(t))
}

func TestWriteVerificationSummariesSources(t *testing.T) {
	t.Parallel()

	st := newSummaryTest(t)
	policy := &provenance.Policy{Mode: provenance.PolicyModeWarn}

	// The same digest promoted from another staging repository, whose
	// policy is not satisfied.
	other := st.edge
	other.SrcRegistry.Name += "-other"
	st.edges[other] = nil
	st.outcomes[st.edge.SrcReference()] = &provenance.ImageProvenance{
		Policies: []*provenance.Policy{policy},
		Results:  []*provenance.PolicyResult{{Satisfied: true, SLSALevel: 3}},
	}
	st.outcomes[other.SrcReference()] = &provenance.ImageProvenance{
		Policies: []*provenance.Policy{policy},
		Results:  []*provenance.PolicyResult{{Violations: []string{"no SLSA build provenance found"}}},
	}

	mfests := st.summaryManifests(t, policy)
	mfests = append(mfests, schema.Manifest{
		SrcRegistry: &other.SrcRegistry,
		Filepath:    mfests[0].Filepath,
		Provenance:  policy,
		Images:      mfests[0].Images,
	})

	require.NoError(t, st.di.WriteVerificationSummaries(
		context.Background(), st.opts, mfests, st.edges, nil, st.outcomes,
	))

	stmt := st.statement(t)
	require.Equal(t, summaryFailed, stmt.Predicate.VerificationResult)
	require.Equal(t, []string{summaryFailed}, stmt.Predicate.VerifiedLevels)
}

func TestWriteVerificationSummariesOtherManifestPolicy(t *testing.T) {
	t.Parallel()

	st := newSummaryTest(t)

	// The policy comes from another manifest, for example of a parent
	// source registry, so the summary names the repository.
	st.outcomes[st.edge.SrcReference()] = &provenance.ImageProvenance{
		Policies: []*provenance.Policy{{Mode: provenance.PolicyModeWarn}},
		Results:  []*provenance.PolicyResult{{Satisfied: true, SLSALevel: 3}},
	}

	require.NoError(t, st.di.WriteVerificationSummaries(
		context.Background(), st.opts, st.summaryManifests(t, nil), st.edges, nil, st.outcomes,
	))

	stmt := st.statement(t)
	require.Equal(t, "git+https://github.com/kubernetes/k8s.io", stmt.Predicate.Policy.URI)
	require.Equal(t, []string{"SLSA_BUILD_LEVEL_3", provenance.LevelManifestReviewed}, stmt.Predicate.VerifiedLevels)
}

func TestWriteVerificationSummariesIndex(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)
	signer := &recordingSigner{}
	di.attSigner = signer

	// An index with two platform manifests, of which the promoter manifest
	// lists only the index and one child.
	idx, err := random.Index(256, 1, 2)
	require.NoError(t, err)

	indexDigest, err := idx.Digest()
	require.NoError(t, err)

	dst := host + "/production/" + testImageApp
	indexRef, err := name.NewDigest(dst + "@" + indexDigest.String())
	require.NoError(t, err)
	require.NoError(t, remote.WriteIndex(indexRef, idx, remote.WithTransport(di.getTransport())))

	im, err := idx.IndexManifest()
	require.NoError(t, err)

	listed, unlisted := im.Manifests[0].Digest.String(), im.Manifests[1].Digest.String()

	src := reg.Context{Name: image.Registry(host + "/staging"), Src: true}
	edge := func(digest string, tag image.Tag) promotion.Edge {
		return promotion.Edge{
			SrcRegistry: src,
			SrcImageTag: promotion.ImageTag{Name: testImageApp, Tag: tag},
			Digest:      image.Digest(digest),
			DstRegistry: reg.Context{Name: image.Registry(host + "/production")},
			DstImageTag: promotion.ImageTag{Name: testImageApp, Tag: tag},
		}
	}

	indexEdge, listedEdge := edge(indexDigest.String(), testTagV1), edge(listed, "")
	edges := map[promotion.Edge]any{indexEdge: nil, listedEdge: nil}
	// The index satisfies its policy through its platform manifests, which
	// were evaluated against their own attestations.
	outcomes := map[string]*provenance.ImageProvenance{
		indexEdge.SrcReference(): {
			Policies: []*provenance.Policy{{Mode: provenance.PolicyModeWarn}},
			Results: []*provenance.PolicyResult{{
				Satisfied: true, SLSALevel: 2, ThroughPlatforms: true,
				Platforms: map[string]*provenance.PolicyResult{
					listed:   {Satisfied: true, SLSALevel: 3},
					unlisted: {Satisfied: true, SLSALevel: 2},
				},
			}},
		},
		listedEdge.SrcReference(): {
			Policies: []*provenance.Policy{{Mode: provenance.PolicyModeWarn}},
			Results:  []*provenance.PolicyResult{{Satisfied: true, SLSALevel: 3}},
		},
	}

	mfests := []schema.Manifest{{
		SrcRegistry: &src,
		Filepath:    gitManifest(t),
		Images: []reg.Image{{
			Name: testImageApp,
			Dmap: reg.DigestTags{image.Digest(indexDigest.String()): {testTagV1}, image.Digest(listed): {}},
		}},
	}}
	opts := &options.Options{SignImages: true, VerificationSummaries: true, MaxSignatureOps: 10}

	// Written once, whatever the number of runs.
	for range 2 {
		require.NoError(t, di.WriteVerificationSummaries(context.Background(), opts, mfests, edges, nil, outcomes))
	}

	levels := map[string][]string{}

	for _, statement := range signer.statements {
		stmt := &summaryPredicate{}
		require.NoError(t, json.Unmarshal(statement, stmt))
		levels[stmt.Predicate.ResourceURI] = stmt.Predicate.VerifiedLevels
	}

	resource := targetIdentity(&indexEdge)
	require.Equal(t, map[string][]string{
		// The lowest level of the platform manifests.
		resource + "@" + indexDigest.String(): {"SLSA_BUILD_LEVEL_2", provenance.LevelManifestReviewed},
		// The listed child has a summary from its own results.
		resource + "@" + listed: {"SLSA_BUILD_LEVEL_3", provenance.LevelManifestReviewed},
		// The unlisted child gets one from its platform results.
		resource + "@" + unlisted: {"SLSA_BUILD_LEVEL_2", provenance.LevelManifestReviewed},
	}, levels)
	require.Len(t, signer.statements, 3)
}

func TestWriteVerificationSummariesSharedChild(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)
	signer := &recordingSigner{}
	di.attSigner = signer

	images := make([]v1.Image, 4)
	digests := make([]string, 4)

	for i := range images {
		img, err := random.Image(256, 1)
		require.NoError(t, err)

		d, err := img.Digest()
		require.NoError(t, err)

		images[i], digests[i] = img, d.String()
	}

	// Two indexes share a child. The second one also holds an attestation
	// manifest, which is no image.
	passed := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: images[0]}, mutate.IndexAddendum{Add: images[1]})
	failed := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: images[0]}, mutate.IndexAddendum{Add: images[2]},
		mutate.IndexAddendum{
			Add:         images[3],
			Annotations: map[string]string{"vnd.docker.reference.type": "attestation-manifest"},
			Platform:    &v1.Platform{OS: "unknown", Architecture: "unknown"},
		})

	dst := host + "/production/" + testImageApp
	src := reg.Context{Name: image.Registry(host + "/staging"), Src: true}
	edges := map[promotion.Edge]any{}
	outcomes := map[string]*provenance.ImageProvenance{}
	dmap := reg.DigestTags{}
	indexDigests := map[string]string{}

	for tag, tc := range map[image.Tag]struct {
		idx     v1.ImageIndex
		outcome *provenance.ImageProvenance
	}{
		"v1": {idx: passed, outcome: &provenance.ImageProvenance{}},
		"v2": {idx: failed, outcome: &provenance.ImageProvenance{
			Policies: []*provenance.Policy{{Mode: provenance.PolicyModeWarn}},
			Results:  []*provenance.PolicyResult{{Violations: []string{"no SLSA build provenance found"}}},
		}},
	} {
		d, err := tc.idx.Digest()
		require.NoError(t, err)

		ref, err := name.NewDigest(dst + "@" + d.String())
		require.NoError(t, err)
		require.NoError(t, remote.WriteIndex(ref, tc.idx, remote.WithTransport(di.getTransport())))

		edge := promotion.Edge{
			SrcRegistry: src,
			SrcImageTag: promotion.ImageTag{Name: testImageApp, Tag: tag},
			Digest:      image.Digest(d.String()),
			DstRegistry: reg.Context{Name: image.Registry(host + "/production")},
			DstImageTag: promotion.ImageTag{Name: testImageApp, Tag: tag},
		}
		edges[edge] = nil
		outcomes[edge.SrcReference()] = tc.outcome
		dmap[edge.Digest] = []image.Tag{tag}
		indexDigests[string(tag)] = d.String()
	}

	mfests := []schema.Manifest{{
		SrcRegistry: &src,
		Filepath:    gitManifest(t),
		Images:      []reg.Image{{Name: testImageApp, Dmap: dmap}},
	}}
	opts := &options.Options{SignImages: true, VerificationSummaries: true, MaxSignatureOps: 10}

	require.NoError(t, di.WriteVerificationSummaries(context.Background(), opts, mfests, edges, nil, outcomes))

	results := map[string]string{}

	for _, statement := range signer.statements {
		stmt := &summaryPredicate{}
		require.NoError(t, json.Unmarshal(statement, stmt))
		require.NotContains(t, results, stmt.Predicate.ResourceURI)
		results[stmt.Predicate.ResourceURI] = stmt.Predicate.VerificationResult
	}

	resource := host + "/production/" + testImageApp + "@"
	require.Equal(t, map[string]string{
		resource + indexDigests["v1"]: summaryPassed,
		resource + indexDigests["v2"]: summaryFailed,
		// The shared child fails with the second index.
		resource + digests[0]: summaryFailed,
		resource + digests[1]: summaryPassed,
		resource + digests[2]: summaryFailed,
	}, results)
}

func TestWriteVerificationSummariesEqualPolicy(t *testing.T) {
	t.Parallel()

	st := newSummaryTest(t)

	// Two manifests of the same source registry declare equal policies,
	// so the policy of the image is that of its own manifest.
	mfests := st.summaryManifests(t, &provenance.Policy{Mode: provenance.PolicyModeWarn})
	mfests = append(mfests, schema.Manifest{
		SrcRegistry: &st.edge.SrcRegistry,
		Filepath:    gitManifest(t),
		Provenance:  &provenance.Policy{Mode: provenance.PolicyModeWarn},
		Images:      []reg.Image{{Name: "other", Dmap: reg.DigestTags{"sha256:other": {testTagV1}}}},
	})
	st.outcomes[st.edge.SrcReference()] = &provenance.ImageProvenance{
		Policies: []*provenance.Policy{mfests[1].Provenance},
		Results:  []*provenance.PolicyResult{{Satisfied: true, SLSALevel: 3}},
	}

	require.NoError(t, st.di.WriteVerificationSummaries(
		context.Background(), st.opts, mfests, st.edges, nil, st.outcomes,
	))
	require.Equal(t, "git+https://github.com/kubernetes/k8s.io#promoter-manifest.yaml", st.statement(t).Predicate.Policy.URI)
}

func TestPolicyContextChildren(t *testing.T) {
	t.Parallel()

	src := reg.Context{Name: "gcr.io/staging", Src: true}
	pc := newPolicyContext([]schema.Manifest{{
		SrcRegistry: &src,
		Filepath:    "promoter-manifest.yaml",
		Images: []reg.Image{{
			Name: testImageApp,
			Dmap: reg.DigestTags{"sha256:index": {testTagV1}},
		}},
	}})

	// A child of the index is not listed, but promoted from the same
	// manifest.
	path, policy := pc.manifest(manifestKey{src: src.Name, name: testImageApp, digest: "sha256:index-child"})
	require.NotNil(t, policy)
	require.Equal(t, "promoter-manifest.yaml", path)
	require.Equal(t, "promoter-manifest.yaml", filepath.Base(policy.GetName()))

	_, policy = pc.manifest(manifestKey{src: src.Name, name: "other", digest: "sha256:index-child"})
	require.Nil(t, policy)
}

func TestWriteVerificationSummariesDisabled(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts func(*options.Options)
	}{
		{name: "off by default", opts: func(o *options.Options) { o.VerificationSummaries = false }},
		{name: "not signing", opts: func(o *options.Options) { o.SignImages = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := newSummaryTest(t)
			tc.opts(st.opts)

			require.NoError(t, st.di.WriteVerificationSummaries(
				context.Background(), st.opts, nil, st.edges, nil, st.outcomes,
			))
			require.Empty(t, st.signer.statements)
			require.Equal(t, 0, st.referrers(t))
		})
	}
}

func TestWriteVerificationSummariesWithoutResult(t *testing.T) {
	t.Parallel()

	st := newSummaryTest(t)

	// An image without a provenance result gets no summary.
	require.NoError(t, st.di.WriteVerificationSummaries(
		context.Background(), st.opts, nil, st.edges, nil, map[string]*provenance.ImageProvenance{},
	))
	require.Empty(t, st.signer.statements)
	require.Equal(t, 0, st.referrers(t))
}
