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
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

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
}

func (f *fakeVerifier) Verify(_ context.Context, _ string) (*provenance.Result, error) {
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

	for _, tc := range []struct {
		shouldErr bool
		msg       string
		prepare   func(*imagefakes.FakePromoterImplementation)
	}{
		{
			shouldErr: false,
			msg:       "All signed",
			prepare:   func(_ *imagefakes.FakePromoterImplementation) {},
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
			msg:       "FixMissingSignatures fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturns(checkresults.Signature{
					"img@sha256:abc": {Missing: []string{"mirror1"}},
				}, nil)
				fpi.FixMissingSignaturesReturns(testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "FixPartialSignatures fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetSignatureStatusReturns(checkresults.Signature{
					"img@sha256:abc": {Signed: []string{"primary"}, Missing: []string{"mirror1"}},
				}, nil)
				fpi.FixPartialSignaturesReturns(testErr)
			},
		},
	} {
		t.Run(tc.msg, func(t *testing.T) {
			sut := imagepromoter.Promoter{}
			mock := imagefakes.FakePromoterImplementation{}
			tc.prepare(&mock)
			sut.SetImplementation(&mock)

			opts := &options.Options{}
			if tc.shouldErr {
				require.Error(t, sut.CheckSignatures(context.Background(), opts), tc.msg)
			} else {
				require.NoError(t, sut.CheckSignatures(context.Background(), opts), tc.msg)
			}
		})
	}
}

func TestCheckSignaturesAllConsistent(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	// Return a result with no missing signatures
	mock.GetSignatureStatusReturns(checkresults.Signature{
		"img@sha256:abc": {Signed: []string{"primary", "mirror1"}},
	}, nil)
	sut.SetImplementation(&mock)

	require.NoError(t, sut.CheckSignatures(context.Background(), &options.Options{}))

	// Should not attempt to fix anything
	require.Equal(t, 0, mock.FixMissingSignaturesCallCount())
	require.Equal(t, 0, mock.FixPartialSignaturesCallCount())
}
