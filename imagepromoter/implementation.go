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
	"github.com/sirupsen/logrus"
	reg "sigs.k8s.io/promo-tools/v3/legacy/dockerregistry"
	"sigs.k8s.io/promo-tools/v3/legacy/gcloud"
)

type defaultPromoterImplementation struct{}

// ValidateManifestLists implements one of the run modes of the promoter
// where it parses the manifests, checks the images and exits
func (di *defaultPromoterImplementation) ValidateManifestLists(opts *Options) error {
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
	printSection("FINISHED (CHECKING MANIFESTS)", true)
	return nil
}

// ValidateOptions checks an options set
func (di *defaultPromoterImplementation) ValidateOptions(opts *Options) error {
	if opts.Snapshot == "" && opts.ManifestBasedSnapshotOf == "" {
		if opts.Manifest == "" && opts.ThinManifestDir == "" {
			return errors.New("either a manifest ot a thin manifest dir have to be set")
		}
	}
	return nil
}

// ActivateServiceAccounts gets key files and activates service accounts
func (di *defaultPromoterImplementation) ActivateServiceAccounts(opts *Options) error {
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
func (di *defaultPromoterImplementation) PrecheckAndExit(
	opts *Options, mfests []reg.Manifest,
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
