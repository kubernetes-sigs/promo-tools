package promoter

import (
	"github.com/pkg/errors"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/promo-tools/v3/legacy/gcloud"
)

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
