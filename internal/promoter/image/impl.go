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

import (
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	reg "sigs.k8s.io/promo-tools/v3/internal/legacy/dockerregistry"
	"sigs.k8s.io/promo-tools/v3/internal/legacy/gcloud"
	"sigs.k8s.io/promo-tools/v3/internal/legacy/stream"
	"sigs.k8s.io/promo-tools/v3/internal/version"
	options "sigs.k8s.io/promo-tools/v3/promoter/image/options"
)

const vulnerabilityDiscalimer = `DISCLAIMER: Vulnerabilities are found as issues with package
binaries within image layers, not necessarily with the image layers themselves.
So a 'fixable' vulnerability may not necessarily be immediately actionable. For
example, even though a fixed version of the binary is available, it doesn't
necessarily mean that a new version of the image layer is available.`

// streamProducerFunc is a function that gets the required fields to
// construct a promotion stream producer
type StreamProducerFunc func(
	srcRegistry reg.RegistryName, srcImageName reg.ImageName,
	destRC reg.RegistryContext, imageName reg.ImageName,
	digest reg.Digest, tag reg.Tag, tp reg.TagOp,
) stream.Producer

type DefaultPromoterImplementation struct{}

// ValidateManifestLists implements one of the run modes of the promoter
// where it parses the manifests, checks the images and exits
func (di *DefaultPromoterImplementation) ValidateManifestLists(opts *options.Options) error {
	registry := reg.RegistryName(opts.Repository)
	images := make([]reg.ImageWithDigestSlice, 0)

	if err := reg.ParseSnapshot(opts.CheckManifestLists, &images); err != nil {
		return errors.Wrap(err, "parsing snapshot")
	}

	imgs, err := reg.FilterParentImages(registry, &images)
	if err != nil {
		return errors.Wrap(err, "filtering parent images")
	}

	reg.ValidateParentImages(registry, imgs)
	di.PrintSection("FINISHED (CHECKING MANIFESTS)", true)
	return nil
}

// ValidateOptions checks an options set
func (di *DefaultPromoterImplementation) ValidateOptions(opts *options.Options) error {
	if opts.Snapshot == "" && opts.ManifestBasedSnapshotOf == "" {
		if opts.Manifest == "" && opts.ThinManifestDir == "" {
			return errors.New("either a manifest ot a thin manifest dir have to be set")
		}
	}
	return nil
}

// ActivateServiceAccounts gets key files and activates service accounts
func (di *DefaultPromoterImplementation) ActivateServiceAccounts(opts *options.Options) error {
	if !opts.UseServiceAcct {
		logrus.Warn("Not setting a service account")
	}
	if err := gcloud.ActivateServiceAccounts(opts.KeyFiles); err != nil {
		return errors.Wrap(err, "activating service accounts")
	}
	// TODO: Output to log the accout used
	return nil
}

// PrecheckAndExit run simple prechecks to exit before promotions
// or security scans
func (di *DefaultPromoterImplementation) PrecheckAndExit(
	opts *options.Options, mfests []reg.Manifest,
) error {
	// Make the sync context tu run the prechecks:
	sc, err := di.MakeSyncContext(opts, mfests)
	if err != nil {
		return errors.Wrap(err, "generatinng sync context for prechecks")
	}

	// Run the prechecks, these will be run and the calling
	// mode of operation should exit.
	return errors.Wrap(
		sc.RunChecks([]reg.PreCheck{}),
		"running prechecks before promotion",
	)
}

func (di *DefaultPromoterImplementation) PrintVersion() {
	logrus.Info(version.Get())
}

// printSection handles the start/finish labels in the
// former legacy cli/run code
func (di *DefaultPromoterImplementation) PrintSection(message string, confirm bool) {
	dryRunLabel := ""
	if !confirm {
		dryRunLabel = "(DRY RUN) "
	}
	logrus.Infof("********** %s %s**********", message, dryRunLabel)
}

// printSecDisclaimer prints a disclaimer about false positives
// that may be found in container image lauyers.
func (di *DefaultPromoterImplementation) PrintSecDisclaimer() {
	logrus.Info(vulnerabilityDiscalimer)
}
