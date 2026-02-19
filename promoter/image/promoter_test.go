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

	// ParseOnly should stop the pipeline after the plan phase without error.
	err := sut.PromoteImages(context.Background(), &options.Options{
		Confirm:   true,
		ParseOnly: true,
	})
	require.NoError(t, err)

	// PromoteImages (on the impl) should never have been called.
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesPipelineDryRun(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	// Confirm=false should stop the pipeline after the validate phase without error.
	err := sut.PromoteImages(context.Background(), &options.Options{
		Confirm: false,
	})
	require.NoError(t, err)

	// ValidateStagingSignatures should have been called (validate phase ran).
	require.Equal(t, 1, mock.ValidateStagingSignaturesCallCount())

	// PromoteImages (on the impl) should never have been called.
	require.Equal(t, 0, mock.PromoteImagesCallCount())
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

func TestPromoteImagesPipelineProvenanceNoopFallback(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	// RequireProvenance=true with no verifier set should use the noop
	// verifier and succeed.
	err := sut.PromoteImages(context.Background(), &options.Options{
		Confirm:           true,
		RequireProvenance: true,
	})
	require.NoError(t, err)
}

func TestPromoteImagesPipelineProvenanceFails(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	mock.GetPromotionEdgesReturns(map[reg.PromotionEdge]interface{}{
		testEdge(): nil,
	}, nil)
	sut.SetImplementation(&mock)

	sut.SetProvenanceVerifier(&fakeVerifier{
		result: &provenance.Result{
			Verified: false,
			Errors:   []string{"no attestation found"},
		},
	})

	err := sut.PromoteImages(context.Background(), &options.Options{
		Confirm:           true,
		RequireProvenance: true,
	})
	require.Error(t, err)

	// Promotion should not have been called.
	require.Equal(t, 0, mock.PromoteImagesCallCount())
}

func TestPromoteImagesPipelineProvenanceVerifierError(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	mock.GetPromotionEdgesReturns(map[reg.PromotionEdge]interface{}{
		testEdge(): nil,
	}, nil)
	sut.SetImplementation(&mock)

	sut.SetProvenanceVerifier(&fakeVerifier{
		err: errors.New("connection refused"),
	})

	err := sut.PromoteImages(context.Background(), &options.Options{
		Confirm:           true,
		RequireProvenance: true,
	})
	require.Error(t, err)
}
