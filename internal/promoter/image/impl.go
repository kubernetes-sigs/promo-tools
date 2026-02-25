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
	"context"
	"errors"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/gcrane"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/release-sdk/sign"

	"sigs.k8s.io/promo-tools/v4/internal/version"
	"sigs.k8s.io/promo-tools/v4/promoter/image/auth"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/vuln"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const vulnerabilityDiscalimer = `DISCLAIMER: Vulnerabilities are found as issues with package
binaries within image layers, not necessarily with the image layers themselves.
So a 'fixable' vulnerability may not necessarily be immediately actionable. For
example, even though a fixed version of the binary is available, it doesn't
necessarily mean that a new version of the image layer is available.`

type DefaultPromoterImplementation struct {
	signer *sign.Signer

	// promotionTransport is the HTTP transport used for image promotion
	// (crane.Copy). If nil, falls back to the global ratelimit.Limiter.
	promotionTransport *ratelimit.RoundTripper

	// signingTransport is the HTTP transport used for signature copy and
	// replication. If nil, falls back to the global ratelimit.Limiter.
	signingTransport *ratelimit.RoundTripper

	// registryProvider abstracts registry operations (read inventory, copy images).
	registryProvider registry.Provider

	// identityTokenProvider abstracts OIDC token generation for signing.
	identityTokenProvider auth.IdentityTokenProvider

	// serviceActivator abstracts service account activation.
	serviceActivator auth.ServiceActivator

	// vulnScanner abstracts vulnerability scanning of container images.
	vulnScanner vuln.Scanner
}

// NewDefaultPromoterImplementation creates a new DefaultPromoterImplementation instance.
func NewDefaultPromoterImplementation(opts *options.Options) *DefaultPromoterImplementation {
	return &DefaultPromoterImplementation{
		signer: sign.New(defaultSignerOptions(opts)),
	}
}

// SetPromotionTransport sets the HTTP transport used for image promotion.
func (di *DefaultPromoterImplementation) SetPromotionTransport(rt *ratelimit.RoundTripper) {
	di.promotionTransport = rt
}

// SetSigningTransport sets the HTTP transport used for signing operations.
func (di *DefaultPromoterImplementation) SetSigningTransport(rt *ratelimit.RoundTripper) {
	di.signingTransport = rt
}

// getSigningTransport returns the transport for signing, falling back to the
// global singleton for backwards compatibility.
func (di *DefaultPromoterImplementation) getSigningTransport() *ratelimit.RoundTripper {
	if di.signingTransport != nil {
		return di.signingTransport
	}
	//nolint:staticcheck // Legacy fallback during transition to BudgetAllocator.
	return ratelimit.Limiter
}

// SetRegistryProvider sets the registry provider for image operations.
func (di *DefaultPromoterImplementation) SetRegistryProvider(p registry.Provider) {
	di.registryProvider = p
}

// SetIdentityTokenProvider sets the OIDC token provider for signing.
func (di *DefaultPromoterImplementation) SetIdentityTokenProvider(p auth.IdentityTokenProvider) {
	di.identityTokenProvider = p
}

// SetServiceActivator sets the service account activator.
func (di *DefaultPromoterImplementation) SetServiceActivator(a auth.ServiceActivator) {
	di.serviceActivator = a
}

// SetVulnScanner sets the vulnerability scanner.
func (di *DefaultPromoterImplementation) SetVulnScanner(s vuln.Scanner) {
	di.vulnScanner = s
}

// defaultSignerOptions returns a new *sign.Options with default values applied.
func defaultSignerOptions(opts *options.Options) *sign.Options {
	signOpts := sign.Default()

	// We want to sign all entities for multi-arch images
	signOpts.Recursive = true

	// Recursive signing can take a bit longer than usual
	signOpts.Timeout = 15 * time.Minute

	// The Certificate Identity to be used to check the images signatures
	signOpts.CertIdentity = opts.SignCheckIdentity

	// The Certificate OICD Issuer to be used to check the images signatures
	signOpts.CertOidcIssuer = opts.SignCheckIssuer

	// A regex Certificate Identity to be used to check the images signatures
	signOpts.CertIdentityRegexp = opts.SignCheckIdentityRegexp

	// A regex to match a Certificate OICD Issuer to be used to check the images signatures
	signOpts.CertOidcIssuerRegexp = opts.SignCheckIssuerRegexp

	return signOpts
}

// ValidateOptions checks an options set.
func (di *DefaultPromoterImplementation) ValidateOptions(opts *options.Options) error {
	if opts.Snapshot == "" && opts.ManifestBasedSnapshotOf == "" {
		if opts.Manifest == "" && opts.ThinManifestDir == "" {
			return errors.New("either a manifest or a thin manifest dir have to be set")
		}
	}
	return nil
}

// ActivateServiceAccounts gets key files and activates service accounts.
func (di *DefaultPromoterImplementation) ActivateServiceAccounts(opts *options.Options) error {
	if !opts.UseServiceAcct {
		logrus.Warn("Not setting a service account")
	}
	return di.serviceActivator.ActivateServiceAccounts(context.Background(), opts.KeyFiles)
}

func (di *DefaultPromoterImplementation) PrintVersion() {
	logrus.Info(version.Get())
}

// printSection handles the start/finish labels in the
// former legacy cli/run code.
func (di *DefaultPromoterImplementation) PrintSection(message string, confirm bool) {
	dryRunLabel := ""
	if !confirm {
		dryRunLabel = "(DRY RUN) "
	}
	logrus.Infof("********** %s %s**********", message, dryRunLabel)
}

// printSecDisclaimer prints a disclaimer about false positives
// that may be found in container image layers.
func (di *DefaultPromoterImplementation) PrintSecDisclaimer() {
	logrus.Info(vulnerabilityDiscalimer)
}

// craneOptions returns common crane options for registry operations,
// including authentication and rate-limited transport.
func (di *DefaultPromoterImplementation) craneOptions() []crane.Option {
	opts := []crane.Option{
		crane.WithAuthFromKeychain(gcrane.Keychain),
		crane.WithUserAgent(image.UserAgent),
	}
	if di.promotionTransport != nil {
		opts = append(opts, crane.WithTransport(di.promotionTransport))
	}
	return opts
}
