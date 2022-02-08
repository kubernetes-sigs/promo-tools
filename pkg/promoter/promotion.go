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

package promoter

import (
	"github.com/pkg/errors"
	reg "sigs.k8s.io/promo-tools/v3/legacy/dockerregistry"
	"sigs.k8s.io/promo-tools/v3/legacy/stream"
)

// This file has all the promoter implementation functions
// related to image promotion.

// ParseManifests reads the manifest file or manifest directory
// and parses them to return a slice of Manifest objects.
func (di *defaultPromoterImplementation) ParseManifests(opts *Options) (mfests []reg.Manifest, err error) {
	// If the options have a manifest file defined, we use that one
	if opts.Manifest != "" {
		mfest, err := reg.ParseManifestFromFile(opts.Manifest)
		if err != nil {
			return mfests, errors.Wrap(err, "parsing the manifest file")
		}

		mfests = []reg.Manifest{mfest}
		// The thin manifests
	} else if opts.ThinManifestDir != "" {
		mfests, err = reg.ParseThinManifestsFromDir(opts.ThinManifestDir)
		if err != nil {
			return nil, errors.Wrap(err, "parsing thin manifest directory")
		}
	}
	return mfests, nil
}

// MakeSyncContext takes a slice of manifests and creates a sync context
// object based on them and the promoter options
func (di defaultPromoterImplementation) MakeSyncContext(
	opts *Options, mfests []reg.Manifest,
) (*reg.SyncContext, error) {
	sc, err := reg.MakeSyncContext(
		mfests, opts.Threads, opts.Confirm, opts.UseServiceAcct,
	)
	if err != nil {
		return nil, errors.Wrap(err, "creating sync context")
	}
	return &sc, err
}

// GetPromotionEdges checks the manifests and determines from
// them the promotion edges, ie the images that need to be
// promoted.
func (di *defaultPromoterImplementation) GetPromotionEdges(
	sc *reg.SyncContext, mfests []reg.Manifest,
) (promotionEdges map[reg.PromotionEdge]interface{}, err error) {
	// First, get the "edges" from the manifests
	promotionEdges, err = reg.ToPromotionEdges(mfests)
	if err != nil {
		return nil, errors.Wrap(
			err, "converting list of manifests to edges for promotion",
		)
	}

	// Run the promotion edge filtering
	promotionEdges, ok := sc.FilterPromotionEdges(promotionEdges, true)
	if !ok {
		// If any funny business was detected during a comparison of the manifests
		// with the state of the registries, then exit immediately.
		return nil, errors.New("encountered errors during edge filtering")
	}
	return promotionEdges, nil
}

// MakeProducerFunction builds a function that will be called
// during promotion to get the producer streams
func (di *defaultPromoterImplementation) MakeProducerFunction(useServiceAccount bool) streamProducerFunc {
	return func(
		srcRegistry reg.RegistryName,
		srcImageName reg.ImageName,
		destRC reg.RegistryContext,
		imageName reg.ImageName,
		digest reg.Digest, tag reg.Tag, tp reg.TagOp,
	) stream.Producer {
		var sp stream.Subprocess
		sp.CmdInvocation = reg.GetWriteCmd(
			destRC, useServiceAccount,
			srcRegistry, srcImageName,
			imageName, digest, tag, tp,
		)
		return &sp
	}
}

// PromoteImages starts an image promotion of a set of edges
func (di *defaultPromoterImplementation) PromoteImages(
	sc *reg.SyncContext,
	promotionEdges map[reg.PromotionEdge]interface{},
	fn streamProducerFunc,
) error {
	if err := sc.Promote(promotionEdges, fn, nil); err != nil {
		return errors.Wrap(err, "running image promotion")
	}
	printSection("END (PROMOTION)", true)
	return nil
}
