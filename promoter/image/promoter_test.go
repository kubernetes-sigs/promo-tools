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
	imagefakes "sigs.k8s.io/promo-tools/v4/promoter/image/imagefakes"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

func TestPromoteImages(t *testing.T) {
	sut := imagepromoter.Promoter{}
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
			prepare:   func(_ *imagefakes.FakePromoterImplementation) {},
		},
		{
			// ValidateOptions fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ValidateOptionsReturns(testErr)
			},
		},
		{
			// ActivateServiceAccounts fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ActivateServiceAccountsReturns(testErr)
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
				fpi.GetPromotionEdgesReturns(nil, testErr)
			},
		},
		{
			// ValidateStagingSignatures fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ValidateStagingSignaturesReturns(nil, testErr)
			},
		},
		{
			// PromoteImages fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.PromoteImagesReturns(testErr)
			},
		},
		{
			// SignImages fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.SignImagesReturns(testErr)
			},
		},
		{
			// ReplicateSignatures fails
			shouldErr: true,
			msg:       "ReplicateSignatures fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ReplicateSignaturesReturns(testErr)
			},
		},
		{
			// WriteSBOMs fails
			shouldErr: true,
			msg:       "WriteSBOMs fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.WriteSBOMsReturns(testErr)
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
	sut.SetImplementation(&mock)

	// non-Confirm should stop after validate phase
	opts := &options.Options{Confirm: false}
	require.NoError(t, sut.PromoteImages(context.Background(), opts))

	// ValidateStagingSignatures should have been called
	require.Equal(t, 1, mock.ValidateStagingSignaturesCallCount())
	// PromoteImages should NOT have been called
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
	// Return a non-empty edge set so provenance has something to check
	mock.GetPromotionEdgesReturns(map[promotion.Edge]any{
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
	mock.GetPromotionEdgesReturns(map[promotion.Edge]any{
		testEdge(): nil,
	}, nil)
	sut.SetImplementation(&mock)

	sut.SetProvenanceVerifier(&fakeVerifier{
		err: errors.New("connection refused"),
	})

	opts := &options.Options{Confirm: true, RequireProvenance: true}
	require.Error(t, sut.PromoteImages(context.Background(), opts))
}

func TestReplicateSignatures(t *testing.T) {
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
			msg:       "EdgesFromManifests fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.EdgesFromManifestsReturns(nil, testErr)
			},
		},
		{
			shouldErr: true,
			msg:       "ReplicateSignatures fails",
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.ReplicateSignaturesReturns(testErr)
			},
		},
	} {
		t.Run(tc.msg, func(t *testing.T) {
			sut := imagepromoter.Promoter{}
			mock := imagefakes.FakePromoterImplementation{}
			tc.prepare(&mock)
			sut.SetImplementation(&mock)

			err := sut.ReplicateSignatures(context.Background(), &options.Options{Confirm: true})
			if tc.shouldErr {
				require.Error(t, err, tc.msg)
			} else {
				require.NoError(t, err, tc.msg)
			}
		})
	}
}

func TestReplicateSignaturesDryRun(t *testing.T) {
	sut := imagepromoter.Promoter{}
	mock := imagefakes.FakePromoterImplementation{}
	sut.SetImplementation(&mock)

	// Without --confirm, should stop after plan phase with no error
	opts := &options.Options{Confirm: false}
	require.NoError(t, sut.ReplicateSignatures(context.Background(), opts))

	// ParseManifests and EdgesFromManifests should have been called
	require.Equal(t, 1, mock.ParseManifestsCallCount())
	require.Equal(t, 1, mock.EdgesFromManifestsCallCount())
	// ReplicateSignatures should NOT have been called
	require.Equal(t, 0, mock.ReplicateSignaturesCallCount())
}

func TestNewPromoter(t *testing.T) {
	p := imagepromoter.New(options.DefaultOptions)
	require.NotNil(t, p)
	require.NotNil(t, p.Options)
}
