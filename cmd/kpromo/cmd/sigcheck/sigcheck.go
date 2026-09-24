/*
Copyright 2023 The Kubernetes Authors.

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

package sigcheck

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	imagepromoter "sigs.k8s.io/promo-tools/v4/promoter/image"
	promoteropts "sigs.k8s.io/promo-tools/v4/promoter/image/options"
)

func Add(parent *cobra.Command) {
	opts := &promoteropts.Options{}
	cmd := &cobra.Command{
		Use:   "sigcheck",
		Short: "Check and repair image signatures and promotion attestations",
		Long: fmt.Sprintf(`sigcheck - Check and repair image signatures and promotion attestations

This subcommand checks that promoted images have their signature and their
promotion attestation. Both are stored on the canonical registry only
(us-central1-docker.pkg.dev/k8s-artifacts-prod/images), registry.k8s.io serves
them from there for all mirrors.

An image is signed when its signature tag has a signature of the identity
set with --certificate-identity or --certificate-identity-regexp (and the
OIDC issuer) for its registry.k8s.io reference. It is attested when it has a
promotion attestation (predicate type https://k8s.io/promo-tools/promotion/v1)
of that identity for its registry.k8s.io reference. Digests promoted without
a tag are only checked for the attestation, because promotion does not sign
them. Signatures, attestations and other artifacts attached to images are not
checked themselves.

Promotion writes attestations since kpromo v4.6.0, which was rolled out to the
production promotion jobs on 2026-09-23. Images uploaded before
--attestations-since (default %s, UTC) are only checked for their
signature, so digests without a tag are not checked at all. Set it to an
earlier date, or to an empty value, to check and repair the attestations of
older images too.

kpromo sigcheck fails when it finds images without a signature or an
attestation. With --confirm, it signs and attests them with the identity of
--signer-account, like promotion does, and fails only if problems remain.

By default, kpromo sigcheck will look at all images promoted during the last
%d days. You can change the default using --from-days and determine a range
using --to-days. For example, to verify all images promoted in an interval
between 10 and 5 days ago run:

   kpromo sigcheck --from-days=10 --to-days=5

To check specific images instead, pass their references:

   kpromo sigcheck registry.k8s.io/kube-apiserver:v1.34.0

To debug the signature checker, you can limit the number of images kpromo
verifies using --limit. When no limit is specified, kpromo will check the
signatures of all images in the specified date range. As an example, to limit
kpromo to the first three images it finds run:

   kpromo sigcheck --limit=3

    `,
			promoteropts.DefaultOptions.SignCheckAttestationsSince,
			promoteropts.DefaultOptions.SignCheckFromDays,
		),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.SignCheckReferences = args
			}

			p := imagepromoter.New(opts)

			return p.CheckSignatures(context.Background(), opts)
		},
	}

	cmd.PersistentFlags().BoolVar(
		&opts.SignCheckFix,
		"confirm",
		false,
		"when true, kpromo will sign and attest images missing a signature or a promotion attestation",
	)

	cmd.PersistentFlags().StringVar(
		&opts.SignerAccount,
		"signer-account",
		promoteropts.DefaultOptions.SignerAccount,
		"service account to use as signing identity",
	)

	cmd.PersistentFlags().IntVar(
		&opts.MaxSignatureOps,
		"max-signature-ops",
		promoteropts.DefaultOptions.MaxSignatureOps,
		"maximum number of concurrent signature operations (at least 1)",
	)

	cmd.PersistentFlags().IntVar(
		&opts.SignCheckFromDays,
		"from-days",
		promoteropts.DefaultOptions.SignCheckFromDays,
		"check images uploaded starting this many days ago",
	)

	cmd.PersistentFlags().IntVar(
		&opts.SignCheckToDays,
		"to-days",
		0,
		"check images --from-days ago to this many days ago (defaults to today)",
	)

	cmd.PersistentFlags().StringVar(
		&opts.SignCheckAttestationsSince,
		"attestations-since",
		promoteropts.DefaultOptions.SignCheckAttestationsSince,
		"check promotion attestations of images uploaded since this date (YYYY-MM-DD, UTC), empty for all images",
	)

	cmd.PersistentFlags().IntVar(
		&opts.SignCheckMaxImages,
		"limit",
		0,
		"limit signature checks to a number of images (defaults to checking all)",
	)

	cmd.PersistentFlags().StringVar(
		&opts.SignCheckIdentity,
		"certificate-identity",
		promoteropts.DefaultOptions.SignCheckIdentity,
		"identity to look for when verifying signatures and attestations",
	)

	cmd.PersistentFlags().StringVar(
		&opts.SignCheckIssuer,
		"certificate-oidc-issuer",
		promoteropts.DefaultOptions.SignCheckIssuer,
		"issuer of the OIDC token used to generate the signature certificate",
	)

	cmd.PersistentFlags().StringVar(
		&opts.SignCheckIdentityRegexp,
		"certificate-identity-regexp",
		"",
		"A regular expression alternative to --certificate-identity. Accepts the Go regular expression syntax described at https://golang.org/s/re2syntax. Either --certificate-identity or --certificate-identity-regexp must be set for keyless flows.",
	)

	cmd.PersistentFlags().StringVar(
		&opts.SignCheckIssuerRegexp,
		"certificate-oidc-issuer-regexp",
		"",
		"A regular expression alternative to --certificate-oidc-issuer. Accepts the Go regular expression syntax described at https://golang.org/s/re2syntax.",
	)

	parent.AddCommand(cmd)
}
