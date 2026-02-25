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
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	impl "sigs.k8s.io/promo-tools/v4/internal/promoter/image"
	"sigs.k8s.io/promo-tools/v4/promoter/image/auth"
	"sigs.k8s.io/promo-tools/v4/promoter/image/checkresults"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/pipeline"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/promoter/image/vuln"
)

var AllowedOutputFormats = []string{
	"csv",
	"yaml",
}

type Promoter struct {
	Options             *options.Options
	impl                promoterImplementation
	budget              *ratelimit.BudgetAllocator
	provenanceVerifier  provenance.Verifier
	provenanceGenerator provenance.Generator
}

func New(opts *options.Options) *Promoter {
	// Create a budget allocator that splits the rate limit between
	// promotion (70%) and signing (30%). After promotion completes,
	// signing gets the full budget via GiveAll.
	budget := ratelimit.NewBudgetAllocator(ratelimit.MaxEvents)
	promoRT := budget.Allocate("promotion", 0.7)
	signRT := budget.Allocate("signing", 0.3)

	di := impl.NewDefaultPromoterImplementation(opts)
	di.SetPromotionTransport(promoRT)
	di.SetSigningTransport(signRT)
	di.SetRegistryProvider(registry.NewCraneProvider(
		registry.WithTransport(promoRT),
	))
	di.SetServiceActivator(&auth.GCPServiceActivator{})
	di.SetIdentityTokenProvider(&auth.GCPIdentityTokenProvider{
		CredentialsFile: opts.SignerInitCredentials,
	})
	di.SetVulnScanner(&vuln.GrafeasScanner{FixableOnly: true})

	p := &Promoter{
		Options: opts,
		impl:    di,
		budget:  budget,
	}

	if opts.GeneratePromotionProvenance {
		p.provenanceGenerator = &provenance.PromotionGenerator{}
	}

	return p
}

func (p *Promoter) SetImplementation(pi promoterImplementation) {
	p.impl = pi
}

// SetProvenanceVerifier sets the provenance verifier used during promotion.
func (p *Promoter) SetProvenanceVerifier(v provenance.Verifier) {
	p.provenanceVerifier = v
}

// SetProvenanceGenerator sets the provenance generator used during attestation.
func (p *Promoter) SetProvenanceGenerator(g provenance.Generator) {
	p.provenanceGenerator = g
}

//counterfeiter:generate . promoterImplementation

// promoterImplementation handles all the functionality in the promoter
// modes of operation.
type promoterImplementation interface {
	// General methods common to all modes of the promoter
	ValidateOptions(*options.Options) error
	ActivateServiceAccounts(*options.Options) error

	// Methods for promotion mode:
	ParseManifests(*options.Options) ([]schema.Manifest, error)
	GetPromotionEdges(*options.Options, []schema.Manifest) (map[promotion.Edge]interface{}, error)
	EdgesFromManifests([]schema.Manifest) (map[promotion.Edge]interface{}, error)
	PromoteImages(*options.Options, map[promotion.Edge]interface{}) error

	// Methods for snapshot mode:
	GetSnapshotSourceRegistry(*options.Options) (*registry.Context, error)
	GetSnapshotManifests(*options.Options) ([]schema.Manifest, error)
	AppendManifestToSnapshot(*options.Options, []schema.Manifest) ([]schema.Manifest, error)
	GetRegistryImageInventory(*options.Options, []schema.Manifest) (registry.RegInvImage, error)
	Snapshot(*options.Options, registry.RegInvImage) error

	// Methods for image vulnerability scans:
	ScanEdges(*options.Options, map[promotion.Edge]interface{}) error

	// Methods for image signing and replication
	PrewarmTUFCache() error
	ValidateStagingSignatures(map[promotion.Edge]interface{}) (map[promotion.Edge]interface{}, error)
	SignImages(*options.Options, map[promotion.Edge]interface{}) error
	ReplicateSignatures(*options.Options, map[promotion.Edge]interface{}) error
	WriteSBOMs(*options.Options, map[promotion.Edge]interface{}) error
	WriteProvenanceAttestations(*options.Options, map[promotion.Edge]interface{}, provenance.Generator) error

	// Methods for checking signatures
	GetLatestImages(*options.Options) ([]string, error)
	GetSignatureStatus(*options.Options, []string) (checkresults.Signature, error)
	FixMissingSignatures(*options.Options, checkresults.Signature) error
	FixPartialSignatures(*options.Options, checkresults.Signature) error

	// Utility functions
	PrintVersion()
	PrintSecDisclaimer()
	PrintSection(string, bool)
}

// PromoteImages is the main method for image promotion.
// It runs by taking all its parameters from a set of options.
func (p *Promoter) PromoteImages(ctx context.Context, opts *options.Options) error {
	// Shared state between pipeline phases, captured by closures.
	var (
		mfests         []schema.Manifest
		promotionEdges map[promotion.Edge]interface{}
	)

	pipe := pipeline.New()

	// Setup phase: validate, activate accounts, prewarm caches.
	pipe.AddPhase(pipeline.NewPhase("setup", func(_ context.Context) error {
		if err := p.impl.ValidateOptions(opts); err != nil {
			return fmt.Errorf("validating options: %w", err)
		}
		if err := p.impl.ActivateServiceAccounts(opts); err != nil {
			return fmt.Errorf("activating service accounts: %w", err)
		}
		if err := p.impl.PrewarmTUFCache(); err != nil {
			return fmt.Errorf("prewarming TUF cache: %w", err)
		}
		return nil
	}))

	// Plan phase: parse manifests and compute edges.
	pipe.AddPhase(pipeline.NewPhase("plan", func(_ context.Context) error {
		var err error
		mfests, err = p.impl.ParseManifests(opts)
		if err != nil {
			return fmt.Errorf("parsing manifests: %w", err)
		}

		p.impl.PrintVersion()
		p.impl.PrintSection("START (PROMOTION)", opts.Confirm)

		promotionEdges, err = p.impl.GetPromotionEdges(opts, mfests)
		if err != nil {
			return fmt.Errorf("computing promotion edges: %w", err)
		}

		if opts.ParseOnly {
			logrus.Info("Manifests parsed, exiting as ParseOnly is set")
			return pipeline.ErrStopPipeline
		}
		return nil
	}))

	// Provenance phase: verify image provenance (optional).
	pipe.AddPhase(pipeline.NewPhase("provenance", func(ctx context.Context) error {
		if !opts.RequireProvenance {
			logrus.Debug("Provenance verification disabled (--require-provenance=false)")
			return nil
		}

		verifier := p.provenanceVerifier
		if verifier == nil {
			verifier = &provenance.NoopVerifier{}
		}

		for edge := range promotionEdges {
			ref := edge.SrcReference()
			if ref == "" {
				continue
			}
			result, err := verifier.Verify(ctx, ref)
			if err != nil {
				return fmt.Errorf("verifying provenance for %s: %w", ref, err)
			}
			if !result.Verified {
				return fmt.Errorf("provenance verification failed for %s: %v",
					ref, result.Errors)
			}
		}
		return nil
	}))

	// Validate phase: check staging signatures.
	pipe.AddPhase(pipeline.NewPhase("validate", func(_ context.Context) error {
		if _, err := p.impl.ValidateStagingSignatures(promotionEdges); err != nil {
			return fmt.Errorf("checking signatures in staging images: %w", err)
		}

		if !opts.Confirm {
			logrus.Info("Dry run complete, exiting before promotion")
			return pipeline.ErrStopPipeline
		}
		return nil
	}))

	// Promote phase: copy images.
	pipe.AddPhase(pipeline.NewPhase("promote", func(_ context.Context) error {
		if err := p.impl.PromoteImages(opts, promotionEdges); err != nil {
			return fmt.Errorf("running promotion: %w", err)
		}

		// Rebalance rate limit budget: give signing the full capacity.
		if p.budget != nil {
			if err := p.budget.GiveAll("signing"); err != nil {
				logrus.WithError(err).Warn("Failed to rebalance rate limit budget")
			}
		}
		return nil
	}))

	// Sign phase: sign promoted images (primary registry only).
	pipe.AddPhase(pipeline.NewPhase("sign", func(_ context.Context) error {
		if err := p.impl.SignImages(opts, promotionEdges); err != nil {
			return fmt.Errorf("signing images: %w", err)
		}
		return nil
	}))

	// Replicate phase: copy signatures to mirror registries.
	pipe.AddPhase(pipeline.NewPhase("replicate", func(_ context.Context) error {
		if err := p.impl.ReplicateSignatures(opts, promotionEdges); err != nil {
			return fmt.Errorf("replicating signatures: %w", err)
		}
		return nil
	}))

	// Attest phase: write SBOMs and provenance attestations.
	pipe.AddPhase(pipeline.NewPhase("attest", func(_ context.Context) error {
		if err := p.impl.WriteSBOMs(opts, promotionEdges); err != nil {
			return fmt.Errorf("writing SBOMs: %w", err)
		}

		if opts.GeneratePromotionProvenance && p.provenanceGenerator != nil {
			if err := p.impl.WriteProvenanceAttestations(opts, promotionEdges, p.provenanceGenerator); err != nil {
				return fmt.Errorf("writing provenance attestations: %w", err)
			}
		}
		return nil
	}))

	return pipe.Run(ctx)
}

// Snapshot runs the steps to output a representation in json or yaml of a registry.
func (p *Promoter) Snapshot(opts *options.Options) (err error) {
	if err := p.impl.ValidateOptions(opts); err != nil {
		return fmt.Errorf("validating options: %w", err)
	}

	if err := p.impl.ActivateServiceAccounts(opts); err != nil {
		return fmt.Errorf("activating service accounts: %w", err)
	}

	p.impl.PrintVersion()
	p.impl.PrintSection("START (SNAPSHOT)", opts.Confirm)

	mfests, err := p.impl.GetSnapshotManifests(opts)
	if err != nil {
		return fmt.Errorf("getting snapshot manifests: %w", err)
	}

	mfests, err = p.impl.AppendManifestToSnapshot(opts, mfests)
	if err != nil {
		return fmt.Errorf("adding the specified manifest to the snapshot context: %w", err)
	}

	rii, err := p.impl.GetRegistryImageInventory(opts, mfests)
	if err != nil {
		return fmt.Errorf("getting registry image inventory: %w", err)
	}

	if err := p.impl.Snapshot(opts, rii); err != nil {
		return fmt.Errorf("generating snapshot: %w", err)
	}
	return nil
}

// SecurityScan runs just like an image promotion, but instead of
// actually copying the new detected images, it will run a vulnerability
// scan on them.
func (p *Promoter) SecurityScan(opts *options.Options) error {
	if err := p.impl.ValidateOptions(opts); err != nil {
		return fmt.Errorf("validating options: %w", err)
	}

	if err := p.impl.ActivateServiceAccounts(opts); err != nil {
		return fmt.Errorf("activating service accounts: %w", err)
	}

	mfests, err := p.impl.ParseManifests(opts)
	if err != nil {
		return fmt.Errorf("parsing manifests: %w", err)
	}

	p.impl.PrintVersion()
	p.impl.PrintSection("START (VULN CHECK)", opts.Confirm)
	p.impl.PrintSecDisclaimer()

	promotionEdges, err := p.impl.GetPromotionEdges(opts, mfests)
	if err != nil {
		return fmt.Errorf("filtering edges: %w", err)
	}

	// TODO: Let's rethink this option
	if opts.ParseOnly {
		logrus.Info("Manifests parsed, exiting as ParseOnly is set")
		return nil
	}

	if !opts.Confirm {
		logrus.Info("Dry run complete, exiting before vulnerability scan")
		return nil
	}

	if err := p.impl.ScanEdges(opts, promotionEdges); err != nil {
		return fmt.Errorf("running vulnerability scan: %w", err)
	}
	return nil
}

// CheckSignatures checks the consistency of a set of images.
func (p *Promoter) CheckSignatures(opts *options.Options) error {
	logrus.Info("Fetching latest promoted images")
	images, err := p.impl.GetLatestImages(opts)
	if err != nil {
		return fmt.Errorf("getting latest promoted images: %w", err)
	}

	logrus.Info("Checking signatures")
	results, err := p.impl.GetSignatureStatus(opts, images)
	if err != nil {
		return fmt.Errorf("checking signature status in images: %w", err)
	}

	if results.TotalPartial() == 0 && results.TotalUnsigned() == 0 {
		logrus.Info("Signature consistency OK!")
		return nil
	}

	logrus.Infof("Fixing %d unsigned images", results.TotalUnsigned())
	if err := p.impl.FixMissingSignatures(opts, results); err != nil {
		return fmt.Errorf("fixing missing signatures: %w", err)
	}

	logrus.Infof("Fixing %d images with partial signatures", results.TotalPartial())
	if err := p.impl.FixPartialSignatures(opts, results); err != nil {
		return fmt.Errorf("fixing partial signatures: %w", err)
	}

	return nil
}

// ReplicateSignatures is a standalone mode that copies image signatures
// from the primary destination registry to all mirror registries.
// Unlike the inline replicate phase in PromoteImages, this method reads
// ALL edges from manifests (not just unsynced ones) so it can run
// independently of the promotion job.
func (p *Promoter) ReplicateSignatures(ctx context.Context, opts *options.Options) error {
	var promotionEdges map[promotion.Edge]interface{}

	// Give the signing transport the full rate limit budget since
	// standalone replication has no promotion or signing workload.
	if p.budget != nil {
		if err := p.budget.GiveAll("signing"); err != nil {
			logrus.WithError(err).Warn("Failed to rebalance rate limit budget")
		}
	}

	pipe := pipeline.New()

	// Setup phase: validate, activate accounts, prewarm caches.
	pipe.AddPhase(pipeline.NewPhase("setup", func(_ context.Context) error {
		if err := p.impl.ValidateOptions(opts); err != nil {
			return fmt.Errorf("validating options: %w", err)
		}
		if err := p.impl.ActivateServiceAccounts(opts); err != nil {
			return fmt.Errorf("activating service accounts: %w", err)
		}
		if err := p.impl.PrewarmTUFCache(); err != nil {
			return fmt.Errorf("prewarming TUF cache: %w", err)
		}
		return nil
	}))

	// Plan phase: parse manifests, compute all edges (unfiltered).
	pipe.AddPhase(pipeline.NewPhase("plan", func(_ context.Context) error {
		mfests, err := p.impl.ParseManifests(opts)
		if err != nil {
			return fmt.Errorf("parsing manifests: %w", err)
		}

		promotionEdges, err = p.impl.EdgesFromManifests(mfests)
		if err != nil {
			return fmt.Errorf("computing edges from manifests: %w", err)
		}

		logrus.Infof("Found %d edges for signature replication", len(promotionEdges))

		if !opts.Confirm {
			logrus.Info("Dry run complete (use --confirm to replicate)")
			return pipeline.ErrStopPipeline
		}
		return nil
	}))

	// Replicate phase: copy signatures to mirror registries.
	pipe.AddPhase(pipeline.NewPhase("replicate", func(_ context.Context) error {
		if err := p.impl.ReplicateSignatures(opts, promotionEdges); err != nil {
			return fmt.Errorf("replicating signatures: %w", err)
		}
		return nil
	}))

	return pipe.Run(ctx)
}
