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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	reg "sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry"
	imagepromoter "sigs.k8s.io/promo-tools/v4/promoter/image"
	imagefakes "sigs.k8s.io/promo-tools/v4/promoter/image/imagefakes"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
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
			// ActivateServiceAccountsrseManifests fails
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
			// MakeSyncContext fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.MakeSyncContextReturns(nil, testErr)
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
				fpi.MakeSyncContextReturns(&reg.SyncContext{UseServiceAccount: true}, nil)
				fpi.ValidateStagingSignaturesReturns(nil, testErr)
			},
		},
		{
			// PromoteImages fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.MakeSyncContextReturns(&reg.SyncContext{UseServiceAccount: true}, nil)
				fpi.PromoteImagesReturns(testErr)
			},
		},
		{
			// SignImages fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.MakeSyncContextReturns(&reg.SyncContext{UseServiceAccount: true}, nil)
				fpi.SignImagesReturns(testErr)
			},
		},
		{
			// WriteSBOMs fails
			shouldErr: true,
			prepare: func(fpi *imagefakes.FakePromoterImplementation) {
				fpi.MakeSyncContextReturns(&reg.SyncContext{UseServiceAccount: true}, nil)
				fpi.WriteSBOMsReturns(testErr)
			},
		},
	} {
		mock := imagefakes.FakePromoterImplementation{}
		tc.prepare(&mock)
		sut.SetImplementation(&mock)
		if tc.shouldErr {
			require.Error(t, sut.PromoteImages(&options.Options{Confirm: true}), tc.msg)
		}
	}
}
