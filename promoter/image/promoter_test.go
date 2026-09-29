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

package imagepromoter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	imagepromoter "sigs.k8s.io/promo-tools/v4/promoter/image"
	"sigs.k8s.io/promo-tools/v4/promoter/image/checkresults"
	imagefakes "sigs.k8s.io/promo-tools/v4/promoter/image/imagefakes"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance/provenancefakes"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// testImage is a tagged image checked by sigcheck.
var testImage = checkresults.Image{Name: "img", Digest: "sha256:abc", Tags: []string{"v1.0"}, Attest: true}

// nonEmptyManifests returns a minimal manifest slice so that the pipeline
// does not stop early due to an empty manifest list.
func nonEmptyManifests() []schema.Manifest {
	return []schema.Manifest{{}}
}

func TestPromoteImages(t *testing.T) {
	sut := imagepromoter.Promoter{}
	sut.SetProvenanceGenerator(&provenance.PromotionGenerator{})
	sut.SetProvenanceVerifier(&fakeVerifier{
		result: &provenance.Result{Verified: true},
	})

	testErr := errors.New("synthetic error")

	for _, tc := range []struct {
		shouldErr bool
		msg       string
		prepare   func(*imagefakes.FakePromoterImplementation)
	}{
		{
			// No errors
			shouldErr: false,
			msg:       "No errors",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ParseManifestsReturns(nonEmptyManifests(), nil)
			},
		},
		{
			// ValidateOptions fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ValidateOptionsReturns(testErr)
			},
		},
		{
			// PrewarmTUFCache fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.PrewarmTUFCacheReturns(testErr)
			},
		},
		{
			// ParseManifests fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ParseManifestsReturns(nil, testErr)
			},
		},
		{
			// GetPromotionEdges fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ParseManifestsReturns(nonEmptyManifests(), nil)
				fpi.GetPromotionEdgesReturns(nil, testErr)
			},
		},
		{
			// ValidateStagingSignatures fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ParseManifestsReturns(nonEmptyManifests(), nil)
				fpi.ValidateStagingSignaturesReturns(nil, testErr)
			},
		},
		{
			// PromoteImages fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ParseManifestsReturns(nonEmptyManifests(), nil)
				fpi.PromoteImagesReturns(testErr)
			},
		},
		{
			// SignImages fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ParseManifestsReturns(nonEmptyManifests(), nil)
				fpi.SignImagesReturns(testErr)
			},
		},
		{
			// WriteProvenanceAttestations fails
			shouldErr: true,
			msg:       "WriteProvenanceAttestations fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ParseManifestsReturns(nonEmptyManifests(), nil)
				fpi.WriteProvenanceAttestationsReturns(testErr)
			},
		},
	} {
		mock := imagefakes.FakePromoterImplementation{}
		tc.prepare(&mock)
		sut.SetImplementation(&mock)

		if tc.shouldErr {
			require.Error(t, sut.PromoteImages(context.Background(), &options.Options{Confirm: true}), tc.msg)
		} else {
			require.NoError(t, sut.PromoteImages(context.Background(), &options.Options{Confirm: true}), tc.msg)
		}
	}
}

func TestPromoteImagesParseOnly(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(nonEmptyManifests(), nil)
	sut.SetImplementation(&mock)

	// ParseOnly should stop after plan phase with no error
	opts := &options.Options{Confirm: true, ParseOnly: true}
	require.NoError(t, sut.PromoteImages(context.Background(), opts))

	// ParseManifests should have been called
	require.Equal(t, 1, mock.ParseManifestsCallCount())
	// PromoteImages should NOT have been called
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesNonConfirm(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(nonEmptyManifests(), nil)
	sut.SetImplementation(&mock)
	sut.SetProvenanceVerifier(&fakeVerifier{
		result: &provenance.Result{Verified: true},
	})

	// non-Confirm should stop after validate phase
	opts := &options.Options{Confirm: false}
	require.NoError(t, sut.PromoteImages(context.Background(), opts))

	// ValidateStagingSignatures should have been called
	require.Equal(t, 1, mock.ValidateStagingSignaturesCallCount())
	// PromoteImages should NOT have been called
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesEmptyManifests(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	// Return empty manifests (e.g., prow diff found no digests)
	mock.ParseManifestsReturns([]schema.Manifest{}, nil)
	sut.SetImplementation(&mock)

	opts := &options.Options{Confirm: true}
	require.NoError(t, sut.PromoteImages(context.Background(), opts))

	// No downstream phases should have been called
	require.Equal(t, 0, mock.GetPromotionEdgesCallCount())
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesProvenanceAlwaysRuns(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(nonEmptyManifests(), nil)
	mock.GetPromotionEdgesReturns(map[promotion.Edge]any{
		testEdge(): nil,
	}, nil)
	sut.SetImplementation(&mock)

	// Set a verifier that returns success
	sut.SetProvenanceVerifier(&fakeVerifier{
		result: &provenance.Result{Verified: true},
	})

	opts := &options.Options{Confirm: true}
	require.NoError(t, sut.PromoteImages(context.Background(), opts))
}

// fakeVerifier implements provenance.Verifier for testing.
type fakeVerifier struct {
	result *provenance.Result
	err    error
	calls  int
}

func (f *fakeVerifier) Verify(_ context.Context, _ string) (*provenance.Result, error) {
	f.calls++

	return f.result, f.err
}

// testEdge returns an Edge with non-empty fields so that
// SrcReference() returns a valid reference string.
func testEdge() promotion.Edge {
	return promotion.Edge{
		SrcRegistry: registry.Context{Name: image.Registry("gcr.io/staging")},
		SrcImageTag: promotion.ImageTag{
			Name: image.Name("test-image"),
			Tag:  image.Tag("v1"),
		},
		Digest: image.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
	}
}

func TestPromoteImagesDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		missing bool
	}{
		{name: "found", err: nil},
		{name: "failed discovery does not block", err: errors.New("registry unavailable")},
		{name: "missing discovery does not block", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sut := imagepromoter.Promoter{}
			mock := imagefakes.FakePromoterImplementation{}
			mock.ParseManifestsReturns(nonEmptyManifests(), nil)

			// A second edge of the same image, promoted to another region.
			other := testEdge()
			other.DstRegistry = registry.Context{Name: image.Registry("gcr.io/other")}

			mock.GetPromotionEdgesReturns(map[promotion.Edge]any{
				testEdge(): nil,
				other:      nil,
			}, nil)
			sut.SetImplementation(&mock)
			sut.SetProvenanceVerifier(&fakeVerifier{
				result: &provenance.Result{Verified: true},
			})

			discoverer := &provenancefakes.FakeDiscoverer{}
			if !tc.missing {
				discoverer.DiscoverReturns(&provenance.Discovery{
					Attestations: []provenance.Attestation{{
						Digest:        string(testEdge().Digest),
						Source:        provenance.SourceReferrer,
						PredicateType: provenance.DefaultPredicateType,
						Status:        provenance.SignatureUnsigned,
					}},
				}, tc.err)
			}

			sut.SetDiscoverer(discoverer)

			require.NoError(t, sut.PromoteImages(context.Background(), &options.Options{Confirm: true}))
			require.Equal(t, 1, discoverer.DiscoverCallCount())

			edge := testEdge()
			_, ref := discoverer.DiscoverArgsForCall(0)
			require.Equal(t, edge.SrcReference(), ref)
			require.Equal(t, 1, mock.PromoteImagesCallCount())

			if tc.err != nil || tc.missing {
				require.Empty(t, sut.Discoveries())
			} else {
				require.Len(t, sut.Discoveries(), 1)
				require.Contains(t, sut.Discoveries(), ref)
			}

			// A run that stops before the provenance phase has none.
			mock.ParseManifestsReturns(nil, nil)
			require.NoError(t, sut.PromoteImages(context.Background(), &options.Options{Confirm: true}))
			require.Nil(t, sut.Discoveries())
		})
	}
}

func TestPromoteImagesProvenanceFails(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(nonEmptyManifests(), nil)
	// Return a non-empty edge set so provenance has something to check
	mock.GetPromotionEdgesReturns(map[promotion.Edge]any{
		testEdge(): nil,
	}, nil)
	sut.SetImplementation(&mock)

	// Set a verifier that returns a verification failure
	sut.SetProvenanceVerifier(&fakeVerifier{
		result: &provenance.Result{
			Verified: false,
			Errors:   []string{"attestation verification failed"},
		},
	})

	opts := &options.Options{Confirm: true}
	require.Error(t, sut.PromoteImages(context.Background(), opts))

	// Promotion should not have been called
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesProvenanceVerifierError(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(nonEmptyManifests(), nil)
	mock.GetPromotionEdgesReturns(map[promotion.Edge]any{
		testEdge(): nil,
	}, nil)
	sut.SetImplementation(&mock)

	sut.SetProvenanceVerifier(&fakeVerifier{
		err: errors.New("connection refused"),
	})

	opts := &options.Options{Confirm: true}
	require.Error(t, sut.PromoteImages(context.Background(), opts))
}

// policyManifests returns a manifest for the test edge's source registry
// with the given provenance policy.
func policyManifests(policy *provenance.Policy) []schema.Manifest {
	return []schema.Manifest{{
		Registries: []registry.Context{{Name: testEdge().SrcRegistry.Name, Src: true}},
		Provenance: policy,
		Filepath:   "manifests/test/promoter-manifest.yaml",
	}}
}

// testProvenancePolicy returns a provenance policy in the given mode.
func testProvenancePolicy(mode provenance.PolicyMode) *provenance.Policy {
	return &provenance.Policy{
		Mode:     mode,
		Signers:  []string{"sigstore::https://accounts.google.com::builder@k8s-staging-test.iam.gserviceaccount.com"},
		Builders: []string{"https://prow.k8s.io/test"},
		Sources:  []string{"github.com/kubernetes/test"},
	}
}

func TestPromoteImagesProvenancePolicy(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        provenance.PolicyMode
		verifierErr error
		wantErr     string
		wantVerify  int
	}{
		{
			name: "require blocks the promotion", mode: provenance.PolicyModeRequire,
			wantErr: "provenance policy not satisfied",
		},
		{name: "warn allows the promotion", mode: provenance.PolicyModeWarn, wantVerify: 1},
		{
			name: "warn keeps the verify-if-present check", mode: provenance.PolicyModeWarn,
			verifierErr: errors.New("tampered attestation"), wantErr: "tampered attestation", wantVerify: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sut := imagepromoter.Promoter{}
			mock := imagefakes.FakePromoterImplementation{}
			mock.ParseManifestsReturns(policyManifests(testProvenancePolicy(tc.mode)), nil)
			mock.GetPromotionEdgesReturns(map[promotion.Edge]any{
				testEdge(): nil,
			}, nil)
			sut.SetImplementation(&mock)

			// Only require mode replaces the verify-if-present check.
			verifier := &fakeVerifier{result: &provenance.Result{Verified: true}, err: tc.verifierErr}
			sut.SetProvenanceVerifier(verifier)

			// No attestations found, so the policy is not satisfied.
			edge := testEdge()

			discoverer := &provenancefakes.FakeDiscoverer{}
			discoverer.DiscoverReturns(&provenance.Discovery{Reference: edge.SrcReference()}, nil)
			sut.SetDiscoverer(discoverer)

			err := sut.PromoteImages(context.Background(), &options.Options{Confirm: true})

			require.Equal(t, 1, discoverer.DiscoverCallCount())
			require.Equal(t, tc.wantVerify, verifier.calls)

			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Equal(t, 0, mock.PromoteImagesCallCount())

				return
			}

			require.NoError(t, err)
			require.Equal(t, 1, mock.PromoteImagesCallCount())
		})
	}
}

func TestPromoteImagesNestedSourceRegistryPolicy(t *testing.T) {
	// A manifest with a parent source registry and a manifest with a
	// nested one promote the same image. Whichever holds the policy, and
	// whatever edge comes first, the policy applies.
	parent := promotion.Edge{
		SrcRegistry: registry.Context{Name: image.Registry("gcr.io/staging"), Src: true},
		SrcImageTag: promotion.ImageTag{Name: image.Name("b/img"), Tag: image.Tag("v1")},
		Digest:      testEdge().Digest,
		DstRegistry: registry.Context{Name: image.Registry("gcr.io/prod-a")},
	}
	nested := promotion.Edge{
		SrcRegistry: registry.Context{Name: image.Registry("gcr.io/staging/b"), Src: true},
		SrcImageTag: promotion.ImageTag{Name: image.Name("img"), Tag: image.Tag("v1")},
		Digest:      testEdge().Digest,
		DstRegistry: registry.Context{Name: image.Registry("gcr.io/prod-b")},
	}

	requirePolicy := testProvenancePolicy(provenance.PolicyModeRequire)

	for _, tc := range []struct {
		name                 string
		parentPol, nestedPol *provenance.Policy
	}{
		{name: "policy on the nested manifest", nestedPol: requirePolicy},
		{name: "policy on the parent manifest", parentPol: requirePolicy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mfests := []schema.Manifest{
				{
					Registries: []registry.Context{parent.SrcRegistry},
					Provenance: tc.parentPol,
					Filepath:   "a/promoter-manifest.yaml",
				},
				{
					Registries: []registry.Context{nested.SrcRegistry},
					Provenance: tc.nestedPol,
					Filepath:   "b/promoter-manifest.yaml",
				},
			}

			for range 10 {
				sut := imagepromoter.Promoter{}
				mock := imagefakes.FakePromoterImplementation{}
				mock.ParseManifestsReturns(mfests, nil)
				mock.GetPromotionEdgesReturns(map[promotion.Edge]any{parent: nil, nested: nil}, nil)
				sut.SetImplementation(&mock)
				sut.SetProvenanceVerifier(&fakeVerifier{result: &provenance.Result{Verified: true}})

				discoverer := &provenancefakes.FakeDiscoverer{}
				discoverer.DiscoverReturns(&provenance.Discovery{Reference: parent.SrcReference()}, nil)
				sut.SetDiscoverer(discoverer)

				err := sut.PromoteImages(context.Background(), &options.Options{Confirm: true})
				require.ErrorContains(t, err, "provenance policy not satisfied")
				require.Equal(t, 0, mock.PromoteImagesCallCount())
			}
		})
	}
}

func TestPromoteImagesProvenancePolicyAllViolations(t *testing.T) {
	// Every image is checked, and all violations are reported at once.
	first := testEdge()
	second := testEdge()
	second.SrcImageTag.Name = image.Name("other-image")

	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(policyManifests(testProvenancePolicy(provenance.PolicyModeRequire)), nil)
	mock.GetPromotionEdgesReturns(map[promotion.Edge]any{first: nil, second: nil}, nil)
	sut.SetImplementation(&mock)
	sut.SetProvenanceVerifier(&fakeVerifier{result: &provenance.Result{Verified: true}})

	discoverer := &provenancefakes.FakeDiscoverer{}
	discoverer.DiscoverStub = func(_ context.Context, ref string) (*provenance.Discovery, error) {
		return &provenance.Discovery{Reference: ref}, nil
	}
	sut.SetDiscoverer(discoverer)

	err := sut.PromoteImages(context.Background(), &options.Options{Confirm: true})
	require.ErrorContains(t, err, first.SrcReference())
	require.ErrorContains(t, err, second.SrcReference())
}

func TestPromoteImagesProvenancePolicyOff(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(policyManifests(&provenance.Policy{Mode: provenance.PolicyModeOff}), nil)
	mock.GetPromotionEdgesReturns(map[promotion.Edge]any{
		testEdge(): nil,
	}, nil)
	sut.SetImplementation(&mock)

	sut.SetProvenanceVerifier(&fakeVerifier{err: errors.New("verified without policy")})

	discoverer := &provenancefakes.FakeDiscoverer{}
	sut.SetDiscoverer(discoverer)

	// Discovery always runs, without a policy the verifier decides.
	err := sut.PromoteImages(context.Background(), &options.Options{Confirm: true})
	require.ErrorContains(t, err, "verified without policy")
	require.Equal(t, 1, discoverer.DiscoverCallCount())
}

func TestPromoteImagesConflictingProvenancePolicies(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}

	mfests := append(
		policyManifests(testProvenancePolicy(provenance.PolicyModeRequire)),
		policyManifests(testProvenancePolicy(provenance.PolicyModeWarn))...,
	)
	mock.ParseManifestsReturns(mfests, nil)
	mock.GetPromotionEdgesReturns(map[promotion.Edge]any{
		testEdge(): nil,
	}, nil)
	sut.SetImplementation(&mock)

	sut.SetProvenanceVerifier(&fakeVerifier{result: &provenance.Result{Verified: true}})

	err := sut.PromoteImages(context.Background(), &options.Options{Confirm: true})
	require.ErrorContains(t, err, "declare different provenance policies")
}

func TestNewPromoter(t *testing.T) {
	p := imagepromoter.New(options.DefaultOptions)
	require.NotNil(t, p)
	require.NotNil(t, p.Options)

	// Verify that a promoter created via New() has the verifier and
	// generator configured by running a full pipeline with a mock impl.
	mock := imagefakes.FakePromoterImplementation{}
	mock.ParseManifestsReturns(nonEmptyManifests(), nil)
	p.SetImplementation(&mock)

	require.NoError(t, p.PromoteImages(context.Background(), &options.Options{Confirm: true}))
	require.Equal(t, 1, mock.WriteProvenanceAttestationsCallCount())
}

func TestSnapshot(t *testing.T) {
	testErr := errors.New("synthetic error")

	for _, tc := range []struct {
		shouldErr bool
		msg       string
		prepare   func(*imagefakes.FakePromoterImplementation)
	}{
		{
			shouldErr: false,
			msg:       "No errors",
			prepare:   func(_ *imagefakes.FakePromoterImplementation) {},
		},
		{
			shouldErr: true,
			msg:       "ValidateOptions fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ValidateOptionsReturns(testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "GetSnapshotManifests fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSnapshotManifestsReturns(nil, testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "AppendManifestToSnapshot fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.AppendManifestToSnapshotReturns(nil, testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "GetRegistryImageInventory fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetRegistryImageInventoryReturns(nil, testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "Snapshot impl fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.SnapshotReturns(testErr)
			},
		},
	} {
		t.Run(tc.msg, func(t *testing.T) {
			sut := imagepromoter.Promoter{}
			mock := imagefakes.FakePromoterImplementation{}
			tc.prepare(&mock)
			sut.SetImplementation(&mock)

			opts := &options.Options{Snapshot: "gcr.io/test"}
			if tc.shouldErr {
				require.Error(t, sut.Snapshot(context.Background(), opts), tc.msg)
			} else {
				require.NoError(t, sut.Snapshot(context.Background(), opts), tc.msg)
			}
		})
	}
}

func TestSecurityScan(t *testing.T) {
	testErr := errors.New("synthetic error")

	for _, tc := range []struct {
		shouldErr bool
		msg       string
		prepare   func(*imagefakes.FakePromoterImplementation)
		opts      *options.Options
	}{
		{
			shouldErr: false,
			msg:       "No errors with confirm",
			prepare:   func(_ *imagefakes.FakePromoterImplementation) {},
			opts:      &options.Options{Confirm: true, SeverityThreshold: 3},
		},
		{
			shouldErr: true,
			msg:       "ValidateOptions fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ValidateOptionsReturns(testErr)
			},
			opts: &options.Options{Confirm: true, SeverityThreshold: 3},
		},
		{
			shouldErr: true,
			msg:       "ParseManifests fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ParseManifestsReturns(nil, testErr)
			},
			opts: &options.Options{Confirm: true, SeverityThreshold: 3},
		},
		{
			shouldErr: true,
			msg:       "GetPromotionEdges fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetPromotionEdgesReturns(nil, testErr)
			},
			opts: &options.Options{Confirm: true, SeverityThreshold: 3},
		},
		{
			shouldErr: true,
			msg:       "ScanEdges fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ScanEdgesReturns(testErr)
			},
			opts: &options.Options{Confirm: true, SeverityThreshold: 3},
		},
	} {
		t.Run(tc.msg, func(t *testing.T) {
			sut := imagepromoter.Promoter{}
			mock := imagefakes.FakePromoterImplementation{}
			tc.prepare(&mock)
			sut.SetImplementation(&mock)

			if tc.shouldErr {
				require.Error(t, sut.SecurityScan(context.Background(), tc.opts), tc.msg)
			} else {
				require.NoError(t, sut.SecurityScan(context.Background(), tc.opts), tc.msg)
			}
		})
	}
}

func TestSecurityScanParseOnly(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	opts := &options.Options{ParseOnly: true, SeverityThreshold: 3}
	require.NoError(t, sut.SecurityScan(context.Background(), opts))

	require.Equal(t, 1, mock.ParseManifestsCallCount())
	require.Equal(t, 0, mock.ScanEdgesCallCount())
}

func TestSecurityScanDryRun(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	opts := &options.Options{Confirm: false, SeverityThreshold: 3}
	require.NoError(t, sut.SecurityScan(context.Background(), opts))

	require.Equal(t, 1, mock.GetPromotionEdgesCallCount())
	require.Equal(t, 0, mock.ScanEdgesCallCount())
}

func TestCheckSignatures(t *testing.T) {
	testErr := errors.New("synthetic error")

	unsigned := checkresults.Results{{Image: testImage, Attested: true}}
	unattested := checkresults.Results{{Image: testImage, Signed: true}}
	consistent := checkresults.Results{{Image: testImage, Signed: true, Attested: true}}

	for _, tc := range []struct {
		shouldErr bool
		msg       string
		confirm   bool
		prepare   func(*imagefakes.FakePromoterImplementation)
	}{
		{
			shouldErr: false,
			msg:       "No images",
			prepare:   func(_ *imagefakes.FakePromoterImplementation) {},
		},
		{
			shouldErr: false,
			msg:       "All signed and attested",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturns(consistent, nil)
			},
		},
		{
			shouldErr: true,
			msg:       "GetLatestImages fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetLatestImagesReturns(nil, testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "GetSignatureStatus fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturns(nil, testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "Unsigned without confirm",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturns(unsigned, nil)
			},
		},
		{
			shouldErr: true,
			msg:       "Unattested without confirm",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturns(unattested, nil)
			},
		},
		{
			shouldErr: true,
			msg:       "FixMissingSignatures fails",
			confirm:   true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturns(unsigned, nil)
				fpi.FixMissingSignaturesReturns(testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "FixMissingAttestations fails",
			confirm:   true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturns(unattested, nil)
				fpi.FixMissingAttestationsReturns(testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "Problems remain after the repair",
			confirm:   true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturns(unsigned, nil)
			},
		},
		{
			shouldErr: true,
			msg:       "Check after the repair fails",
			confirm:   true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturnsOnCall(0, unsigned, nil)
				fpi.GetSignatureStatusReturnsOnCall(1, nil, testErr)
			},
		},
		{
			shouldErr: false,
			msg:       "Repaired",
			confirm:   true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturnsOnCall(0, unsigned, nil)
				fpi.GetSignatureStatusReturnsOnCall(1, consistent, nil)
			},
		},
	} {
		t.Run(tc.msg, func(t *testing.T) {
			sut := imagepromoter.Promoter{}
			mock := imagefakes.FakePromoterImplementation{}
			tc.prepare(&mock)
			sut.SetImplementation(&mock)

			opts := &options.Options{SignCheckFix: tc.confirm, MaxSignatureOps: 1}
			if tc.shouldErr {
				require.Error(t, sut.CheckSignatures(context.Background(), opts), tc.msg)
			} else {
				require.NoError(t, sut.CheckSignatures(context.Background(), opts), tc.msg)
			}
		})
	}
}

func TestCheckSignaturesInvalidOptions(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	// errgroup blocks forever with a limit of 0.
	require.Error(t, sut.CheckSignatures(context.Background(), &options.Options{SignCheckFix: true}))
	require.Equal(t, 0, mock.GetLatestImagesCallCount())
}

func TestCheckSignaturesAllConsistent(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	tagless := checkresults.Image{Name: "img", Digest: "sha256:def", Attest: true}
	old := checkresults.Image{Name: "old", Digest: "sha256:123", Tags: []string{"v0.1"}}
	mock.GetSignatureStatusReturns(checkresults.Results{
		{Image: testImage, Signed: true, Attested: true},
		// Images without tags are not signed by promotion.
		{Image: tagless, Attested: true},
		// Images promoted before attestations are not attested.
		{Image: old, Signed: true},
	}, nil)
	sut.SetImplementation(&mock)

	require.NoError(t, sut.CheckSignatures(context.Background(), &options.Options{SignCheckFix: true, MaxSignatureOps: 1}))

	// Should not attempt to fix anything
	require.Equal(t, 0, mock.FixMissingSignaturesCallCount())
	require.Equal(t, 0, mock.FixMissingAttestationsCallCount())
}

func TestCheckSignaturesRepair(t *testing.T) {
	sut := imagepromoter.Promoter{}
	sut.SetProvenanceGenerator(&provenance.PromotionGenerator{})

	mock := imagefakes.FakePromoterImplementation{}

	other := checkresults.Image{Name: "other", Digest: "sha256:def", Tags: []string{"v2.0"}, Attest: true}
	signed := checkresults.Status{Image: testImage, Signed: true, Attested: true}
	unsigned := checkresults.Status{Image: other, Attested: true}

	mock.GetSignatureStatusReturnsOnCall(0, checkresults.Results{signed, unsigned}, nil)
	mock.GetSignatureStatusReturnsOnCall(1, checkresults.Results{{
		Image: unsigned.Image, Signed: true, Attested: true,
	}}, nil)
	sut.SetImplementation(&mock)

	require.NoError(t, sut.CheckSignatures(context.Background(), &options.Options{SignCheckFix: true, MaxSignatureOps: 1}))

	// Only the problems are repaired and checked again.
	require.Equal(t, 1, mock.FixMissingSignaturesCallCount())
	_, _, repaired := mock.FixMissingSignaturesArgsForCall(0)
	require.Equal(t, checkresults.Results{unsigned}, repaired)

	require.Equal(t, 1, mock.FixMissingAttestationsCallCount())
	_, _, repaired, generator := mock.FixMissingAttestationsArgsForCall(0)
	require.Equal(t, checkresults.Results{unsigned}, repaired)
	require.NotNil(t, generator)

	require.Equal(t, 2, mock.GetSignatureStatusCallCount())
	_, _, rechecked := mock.GetSignatureStatusArgsForCall(1)
	require.Equal(t, []checkresults.Image{unsigned.Image}, rechecked)
}
