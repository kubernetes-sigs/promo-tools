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

package imagepromoter

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

import (
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	reg "sigs.k8s.io/promo-tools/v3/internal/legacy/dockerregistry"
	impl "sigs.k8s.io/promo-tools/v3/internal/promoter/image"
	options "sigs.k8s.io/promo-tools/v3/promoter/image/options"
)

var AllowedOutputFormats = []string{
	"csv",
	"yaml",
}

type Promoter struct {
	Options *options.Options
	impl    promoterImplementation
}

func New() *Promoter {
	return &Promoter{
		Options: options.DefaultOptions,
		impl:    &impl.DefaultPromoterImplementation{},
	}
}

//counterfeiter:generate . promoterImplementation

// promoterImplementation handles all the functionality in the promoter
// modes of operation.
type promoterImplementation interface {
	// General methods common to all modes of the promoter
	ValidateOptions(*options.Options) error
	ActivateServiceAccounts(*options.Options) error
	PrecheckAndExit(*options.Options, []reg.Manifest) error

	// Methods for promotion mode:
	ParseManifests(*options.Options) ([]reg.Manifest, error)
	MakeSyncContext(*options.Options, []reg.Manifest) (*reg.SyncContext, error)
	GetPromotionEdges(*reg.SyncContext, []reg.Manifest) (map[reg.PromotionEdge]interface{}, error)
	MakeProducerFunction(bool) impl.StreamProducerFunc
	PromoteImages(*reg.SyncContext, map[reg.PromotionEdge]interface{}, impl.StreamProducerFunc) error

	// Methods for snapshot mode:
	GetSnapshotSourceRegistry(*options.Options) (*reg.RegistryContext, error)
	GetSnapshotManifests(*options.Options) ([]reg.Manifest, error)
	AppendManifestToSnapshot(*options.Options, []reg.Manifest) ([]reg.Manifest, error)
	GetRegistryImageInventory(*options.Options, []reg.Manifest) (reg.RegInvImage, error)
	Snapshot(*options.Options, reg.RegInvImage) error

	// Methods for image vulnerability scans:
	ScanEdges(*options.Options, *reg.SyncContext, map[reg.PromotionEdge]interface{}) error

	// Methods for manifest list verification:
	ValidateManifestLists(opts *options.Options) error

	// Methods for image signing
	ValidateStagingSignatures(map[reg.PromotionEdge]interface{}) error
	SignImages(*options.Options, *reg.SyncContext, map[reg.PromotionEdge]interface{}) error
	WriteSBOMs(*options.Options, *reg.SyncContext, map[reg.PromotionEdge]interface{}) error

	// Utility functions
	PrintVersion()
	PrintSecDisclaimer()
	PrintSection(string, bool)
}

// PromoteImages is the main method for image promotion
// it runs by taking all its parameters from a set of options.
func (p *Promoter) PromoteImages(opts *options.Options) (err error) {
	// Validate the options. Perhaps another image-specific
	// validation function may be needed.
	if err := p.impl.ValidateOptions(opts); err != nil {
		return errors.Wrap(err, "validating options")
	}

	if err := p.impl.ActivateServiceAccounts(opts); err != nil {
		return errors.Wrap(err, "activating service accounts")
	}

	mfests, err := p.impl.ParseManifests(opts)
	if err != nil {
		return errors.Wrap(err, "parsing manifests")
	}

	p.impl.PrintVersion()
	p.impl.PrintSection("START (PROMOTION)", opts.Confirm)

	sc, err := p.impl.MakeSyncContext(opts, mfests)
	if err != nil {
		return errors.Wrap(err, "creating sync context")
	}

	promotionEdges, err := p.impl.GetPromotionEdges(sc, mfests)
	if err != nil {
		return errors.Wrap(err, "filtering edges")
	}

	// MakeProducer
	producerFunc := p.impl.MakeProducerFunction(sc.UseServiceAccount)

	// TODO: Let's rethink this option
	if opts.ParseOnly {
		logrus.Info("Manifests parsed, exiting as ParseOnly is set")
		return nil
	}

	// Verify any signatures in staged images
	if err := p.impl.ValidateStagingSignatures(promotionEdges); err != nil {
		return errors.Wrap(err, "checking signtaures in staging images")
	}

	// Check the pull request
	if !opts.Confirm {
		return p.impl.PrecheckAndExit(opts, mfests)
	}

	if err := p.impl.PromoteImages(sc, promotionEdges, producerFunc); err != nil {
		return errors.Wrap(err, "running promotion")
	}

	if err := p.impl.SignImages(opts, sc, promotionEdges); err != nil {
		return errors.Wrap(err, "signing images")
	}

	if err := p.impl.WriteSBOMs(opts, sc, promotionEdges); err != nil {
		return errors.Wrap(err, "signing images")
	}

	return nil
}

// Snapshot runs the steps to output a representation in json or yaml of a registry
func (p *Promoter) Snapshot(opts *options.Options) (err error) {
	if err := p.impl.ValidateOptions(opts); err != nil {
		return errors.Wrap(err, "validating options")
	}

	if err := p.impl.ActivateServiceAccounts(opts); err != nil {
		return errors.Wrap(err, "activating service accounts")
	}

	p.impl.PrintVersion()
	p.impl.PrintSection("START (SNAPSHOT)", opts.Confirm)

	mfests, err := p.impl.GetSnapshotManifests(opts)
	if err != nil {
		return errors.Wrap(err, "getting snapshot manifests")
	}

	mfests, err = p.impl.AppendManifestToSnapshot(opts, mfests)
	if err != nil {
		return errors.Wrap(err, "adding the specified manifest to the snapshot context")
	}

	rii, err := p.impl.GetRegistryImageInventory(opts, mfests)
	if err != nil {
		return errors.Wrap(err, "getting registry image inventory")
	}

	return errors.Wrap(p.impl.Snapshot(opts, rii), "generating snapshot")
}

// SecurityScan runs just like an image promotion, but instead of
// actually copying the new detected images, it will run a vulnerability
// scan on them
func (p *Promoter) SecurityScan(opts *options.Options) error {
	if err := p.impl.ValidateOptions(opts); err != nil {
		return errors.Wrap(err, "validating options")
	}

	if err := p.impl.ActivateServiceAccounts(opts); err != nil {
		return errors.Wrap(err, "activating service accounts")
	}

	mfests, err := p.impl.ParseManifests(opts)
	if err != nil {
		return errors.Wrap(err, "parsing manifests")
	}

	p.impl.PrintVersion()
	p.impl.PrintSection("START (VULN CHECK)", opts.Confirm)
	p.impl.PrintSecDisclaimer()

	sc, err := p.impl.MakeSyncContext(opts, mfests)
	if err != nil {
		return errors.Wrap(err, "creating sync context")
	}

	promotionEdges, err := p.impl.GetPromotionEdges(sc, mfests)
	if err != nil {
		return errors.Wrap(err, "filtering edges")
	}

	// TODO: Let's rethink this option
	if opts.ParseOnly {
		logrus.Info("Manifests parsed, exiting as ParseOnly is set")
		return nil
	}

	// Check the pull request
	if !opts.Confirm {
		return p.impl.PrecheckAndExit(opts, mfests)
	}

	return errors.Wrap(
		p.impl.ScanEdges(opts, sc, promotionEdges),
		"running vulnerability scan",
	)
}

// CheckManifestLists is a mode that just checks manifests
// and exists.
func (p *Promoter) CheckManifestLists(opts *options.Options) error {
	if err := p.impl.ValidateOptions(opts); err != nil {
		return errors.Wrap(err, "validating options")
	}

	if err := p.impl.ActivateServiceAccounts(opts); err != nil {
		return errors.Wrap(err, "activating service accounts")
	}

	return errors.Wrap(
		p.impl.ValidateManifestLists(opts), "checking manifest lists",
	)
}
