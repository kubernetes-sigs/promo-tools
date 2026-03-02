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
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/gcrane"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/release-sdk/sign"
	"sigs.k8s.io/release-utils/version"

	"sigs.k8s.io/promo-tools/v4/promoter/image/auth"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/promoter/image/vuln"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const vulnerabilityDisclaimer = `DISCLAIMER: Vulnerabilities are found as issues with package
binaries within image layers, not necessarily with the image layers themselves.
So a 'fixable' vulnerability may not necessarily be immediately actionable. For
example, even though a fixed version of the binary is available, it doesn't
necessarily mean that a new version of the image layer is available.`

type DefaultPromoterImplementation struct {
	signer *sign.Signer

	// transport is the rate-limited HTTP transport shared by all phases.
	transport *ratelimit.RoundTripper

	// puller reuses HTTP auth/transport state across pull operations.
	puller *remote.Puller

	// pusher reuses HTTP auth/transport state across push operations.
	pusher *remote.Pusher

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

// SetTransport sets the rate-limited HTTP transport for all phases
// and initializes the shared puller/pusher for connection reuse.
func (di *DefaultPromoterImplementation) SetTransport(rt *ratelimit.RoundTripper) {
	di.transport = rt
	di.initRemote()
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

	if err := di.serviceActivator.ActivateServiceAccounts(context.Background(), opts.KeyFiles); err != nil {
		return fmt.Errorf("activating service accounts: %w", err)
	}

	return nil
}

func (di *DefaultPromoterImplementation) PrintVersion() {
	v := version.GetVersionInfo()
	logrus.Infof(
		"kpromo %s (commit: %s, built: %s, go: %s)",
		v.GitVersion, v.GitCommit, v.BuildDate, v.GoVersion,
	)
}

// PrintSecDisclaimer prints a disclaimer about false positives
// that may be found in container image layers.
func (di *DefaultPromoterImplementation) PrintSecDisclaimer() {
	logrus.Info(vulnerabilityDisclaimer)
}

// getTransport returns the rate-limited transport.
func (di *DefaultPromoterImplementation) getTransport() *ratelimit.RoundTripper {
	return di.transport
}

// initRemote creates a shared puller and pusher that reuse HTTP
// auth/transport state across OCI operations.
func (di *DefaultPromoterImplementation) initRemote() {
	remoteOpts := di.remoteOptions()

	if p, err := remote.NewPuller(remoteOpts...); err == nil {
		di.puller = p
	} else {
		logrus.Warnf("Failed to create shared puller: %v", err)
	}

	if p, err := remote.NewPusher(remoteOpts...); err == nil {
		di.pusher = p
	} else {
		logrus.Warnf("Failed to create shared pusher: %v", err)
	}
}

// remoteOptions returns common remote options for OCI operations,
// including authentication, user-agent, transport, and reuse of
// puller/pusher state when available.
func (di *DefaultPromoterImplementation) remoteOptions() []remote.Option {
	opts := []remote.Option{
		remote.WithAuthFromKeychain(gcrane.Keychain),
		remote.WithUserAgent(image.UserAgent),
	}

	if di.transport != nil {
		opts = append(opts, remote.WithTransport(di.transport))
	}

	if di.puller != nil {
		opts = append(opts, remote.Reuse(di.puller))
	}

	if di.pusher != nil {
		opts = append(opts, remote.Reuse(di.pusher))
	}

	return opts
}

// craneOptions returns common crane options for registry operations,
// including authentication, rate-limited transport, and reuse of
// puller/pusher state when available.
func (di *DefaultPromoterImplementation) craneOptions() []crane.Option {
	opts := []crane.Option{
		crane.WithAuthFromKeychain(gcrane.Keychain),
		crane.WithUserAgent(image.UserAgent),
	}

	if di.transport != nil {
		opts = append(opts, crane.WithTransport(di.transport))
	}

	if di.puller != nil || di.pusher != nil {
		var remoteOpts []remote.Option

		if di.puller != nil {
			remoteOpts = append(remoteOpts, remote.Reuse(di.puller))
		}

		if di.pusher != nil {
			remoteOpts = append(remoteOpts, remote.Reuse(di.pusher))
		}

		opts = append(opts, func(o *crane.Options) {
			o.Remote = append(o.Remote, remoteOpts...)
		})
	}

	return opts
}
