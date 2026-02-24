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

	reg "sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry/registry"
	imagepromoter "sigs.k8s.io/promo-tools/v4/promoter/image"
	imagefakes "sigs.k8s.io/promo-tools/v4/promoter/image/imagefakes"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// promoteImagesErrorCases returns the common error injection test cases
// used by both pipeline and legacy path tests.
func promoteImagesErrorCases() []struct {
	shouldErr bool
	msg       string
	prepare   func(*imagefakes.FakePromoterImplementation)
} {
	testErr := errors.New("synthetic error")
	return []struct {
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
			msg:       "ActivateServiceAccounts fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ActivateServiceAccountsReturns(testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "PrewarmTUFCache fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.PrewarmTUFCacheReturns(testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "ParseManifests fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ParseManifestsReturns(nil, testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "MakeSyncContext fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.MakeSyncContextReturns(nil, testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "GetPromotionEdges fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.GetPromotionEdgesReturns(nil, testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "ValidateStagingSignatures fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.MakeSyncContextReturns(&reg.SyncContext{UseServiceAccount: true}, nil)
				fpi.ValidateStagingSignaturesReturns(nil, testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "PromoteImages fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.MakeSyncContextReturns(&reg.SyncContext{UseServiceAccount: true}, nil)
				fpi.PromoteImagesReturns(testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "SignImages fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.MakeSyncContextReturns(&reg.SyncContext{UseServiceAccount: true}, nil)
				fpi.SignImagesReturns(testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "ReplicateSignatures fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.MakeSyncContextReturns(&reg.SyncContext{UseServiceAccount: true}, nil)
				fpi.ReplicateSignaturesReturns(testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "WriteSBOMs fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.MakeSyncContextReturns(&reg.SyncContext{UseServiceAccount: true}, nil)
				fpi.WriteSBOMsReturns(testErr)
			},
		},
	}
}

func TestPromoteImagesPipeline(t *testing.T) {
	for _, tc := range promoteImagesErrorCases() {
		t.Run(tc.msg, func(t *testing.T) {
			sut := imagepromoter.Promoter{}
			mock := imagefakes.FakePromoterImplementation{}
			tc.prepare(&mock)
			sut.SetImplementation(&mock)
			err := sut.PromoteImages(context.Background(), &options.Options{Confirm: true})
			if tc.shouldErr {
				require.Error(t, err, tc.msg)
			} else {
				require.NoError(t, err, tc.msg)
			}
		})
	}
}

func TestPromoteImagesLegacy(t *testing.T) {
	for _, tc := range promoteImagesErrorCases() {
		t.Run(tc.msg, func(t *testing.T) {
			sut := imagepromoter.Promoter{}
			mock := imagefakes.FakePromoterImplementation{}
			tc.prepare(&mock)
			sut.SetImplementation(&mock)
			err := sut.PromoteImages(context.Background(), &options.Options{
				Confirm:           true,
				UseLegacyPipeline: true,
			})
			if tc.shouldErr {
				require.Error(t, err, tc.msg)
			} else {
				require.NoError(t, err, tc.msg)
			}
		})
	}
}

func TestPromoteImagesPipelineParseOnly(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	// Pipeline path: ParseOnly should stop after plan phase with no error
	opts := &options.Options{Confirm: true, ParseOnly: true}
	require.NoError(t, sut.PromoteImages(context.Background(), opts))

	// ParseManifests should have been called
	require.Equal(t, 1, mock.ParseManifestsCallCount())
	// PromoteImages should NOT have been called
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesLegacyParseOnly(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	// Legacy path: ParseOnly should stop after parsing with no error
	opts := &options.Options{
		Confirm:           true,
		ParseOnly:         true,
		UseLegacyPipeline: true,
	}
	require.NoError(t, sut.PromoteImages(context.Background(), opts))
	require.Equal(t, 1, mock.ParseManifestsCallCount())
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesPipelineDryRun(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	// Pipeline path: non-Confirm should stop after validate (precheck)
	opts := &options.Options{Confirm: false}
	require.NoError(t, sut.PromoteImages(context.Background(), opts))

	// ValidateStagingSignatures should have been called
	require.Equal(t, 1, mock.ValidateStagingSignaturesCallCount())
	// PrecheckAndExit should have been called
	require.Equal(t, 1, mock.PrecheckAndExitCallCount())
	// PromoteImages should NOT have been called
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesLegacyDryRun(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	// Legacy path: non-Confirm should stop after precheck
	opts := &options.Options{Confirm: false, UseLegacyPipeline: true}
	require.NoError(t, sut.PromoteImages(context.Background(), opts))

	require.Equal(t, 1, mock.PrecheckAndExitCallCount())
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesProvenanceDisabled(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	// Provenance disabled (default): should proceed without calling verifier
	opts := &options.Options{Confirm: true, RequireProvenance: false}
	require.NoError(t, sut.PromoteImages(context.Background(), opts))
}

func TestPromoteImagesProvenanceEnabled(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	// When provenance is required but no verifier is set, the noop verifier
	// is used (always passes).
	opts := &options.Options{Confirm: true, RequireProvenance: true}
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

// testEdge returns a PromotionEdge with non-empty fields so that
// SrcReference() returns a valid reference string.
func testEdge() reg.PromotionEdge {
	return reg.PromotionEdge{
		SrcRegistry: registry.Context{Name: image.Registry("gcr.io/staging")},
		SrcImageTag: reg.ImageTag{
			Name: image.Name("test-image"),
			Tag:  image.Tag("v1"),
		},
		Digest: image.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
	}
}

func TestPromoteImagesProvenanceFails(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	// Return a non-empty edge set so provenance has something to check
	mock.GetPromotionEdgesReturns(map[reg.PromotionEdge]interface{}{
		testEdge(): nil,
	}, nil)
	sut.SetImplementation(&mock)

	// Set a verifier that returns a verification failure
	sut.SetProvenanceVerifier(&fakeVerifier{
		result: &provenance.Result{
			Verified: false,
			Errors:   []string{"no attestation found"},
		},
	})

	opts := &options.Options{Confirm: true, RequireProvenance: true}
	require.Error(t, sut.PromoteImages(context.Background(), opts))

	// Promotion should not have been called
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesProvenanceVerifierError(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	mock.GetPromotionEdgesReturns(map[reg.PromotionEdge]interface{}{
		testEdge(): nil,
	}, nil)
	sut.SetImplementation(&mock)

	sut.SetProvenanceVerifier(&fakeVerifier{
		err: errors.New("connection refused"),
	})

	opts := &options.Options{Confirm: true, RequireProvenance: true}
	require.Error(t, sut.PromoteImages(context.Background(), opts))
}

func TestNewPromoter(t *testing.T) {
	p := imagepromoter.New(options.DefaultOptions)
	require.NotNil(t, p)
	require.NotNil(t, p.Options)
}
