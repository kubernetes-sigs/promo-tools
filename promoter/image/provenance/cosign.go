/*
Copyright 2026 The Kubernetes Authors.

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

package provenance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/gcrane"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrremote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	cosignverify "github.com/sigstore/cosign/v3/cmd/cosign/cli/verify"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	attestationTagSuffix = ".att"

	// DefaultPredicateType is the predicate type verified when the
	// verifier is not configured with one. Cosign requires a predicate
	// type and rejects the verification outright when it is empty.
	DefaultPredicateType = "https://slsa.dev/provenance/v1"

	// anyIdentity matches every certificate identity and OIDC issuer. It
	// is used for the second verification pass, see Verify.
	anyIdentity = ".*"

	// predicateMismatchMessage is the error cosign returns when every
	// attestation verified, but none of them carries the requested
	// predicate type. Cosign has no typed error for this, so the message
	// has to be matched, see cmd/cosign/cli/verify/verify_attestation.go
	// and re-check it when bumping cosign.
	predicateMismatchMessage = "none of the attestations matched the predicate type"
)

// CosignVerifier verifies provenance attestations attached to container images
// using the cosign attestation tag convention.
//
// It uses verify-if-present semantics: when an attestation tag exists it
// is cryptographically verified using cosign; when no attestation is
// found a warning is logged and the image is still allowed through.
// Attestations signed by identities other than the configured ones are
// also allowed through, as long as they verify cryptographically.
type CosignVerifier struct {
	// CertIdentity is the expected certificate identity for attestation
	// verification (e.g., "krel-trust@k8s-releng-prod.iam.gserviceaccount.com").
	// It is ignored when CertIdentityRegexp is set, because cosign rejects
	// both at once.
	CertIdentity string

	// CertIdentityRegexp is a regex alternative to CertIdentity. It takes
	// precedence over CertIdentity.
	CertIdentityRegexp string

	// CertOidcIssuer is the expected OIDC issuer for the signing identity
	// (e.g., "https://accounts.google.com"). It is ignored when
	// CertOidcIssuerRegexp is set.
	CertOidcIssuer string

	// CertOidcIssuerRegexp is a regex alternative to CertOidcIssuer. It
	// takes precedence over CertOidcIssuer.
	CertOidcIssuerRegexp string

	// PredicateType is the attestation predicate type to verify. Defaults
	// to DefaultPredicateType.
	PredicateType string

	// Transport is the HTTP transport used for registry requests. Set it
	// to the promoter transport so that verification shares the global
	// rate limit. Defaults to the crane default transport.
	Transport http.RoundTripper

	// verifyFn runs a single cosign verification pass. It exists so that
	// tests can exercise the decision logic without reaching sigstore.
	verifyFn func(ctx context.Context, ref string, identity, issuer identityOptions) error
}

// Verify checks whether the image has a valid provenance attestation attached.
// It first checks for the attestation tag existence, then verifies the
// attestation signature using cosign.
//
// Cosign reports "signed by an identity we do not trust" and "signed by a
// trusted identity but broken" as the same ErrNoMatchingAttestations. To
// tell them apart, an attestation that does not match the configured
// identities is verified a second time accepting any identity. Promotion is
// blocked only when nothing verifies at all, which means the attestation is
// malformed or tampered with.
func (v *CosignVerifier) Verify(ctx context.Context, ref string) (*Result, error) {
	result := &Result{}

	parsedRef, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("parsing reference %q: %w", ref, err)
	}

	// Extract the digest to derive the attestation tag.
	digest, ok := parsedRef.(name.Digest)
	if !ok {
		return nil, fmt.Errorf("reference %q must include a digest", ref)
	}

	attTag := digestToAttestationTag(image.Digest(digest.DigestStr()))
	attRef := fmt.Sprintf(
		"%s/%s:%s",
		digest.Context().RegistryStr(),
		digest.Context().RepositoryStr(),
		attTag,
	)

	logrus.Debugf("Checking attestation at %s", attRef)

	// Check if the attestation tag exists.
	craneOpts := []crane.Option{
		crane.WithAuthFromKeychain(gcrane.Keychain),
		crane.WithUserAgent(image.UserAgent),
	}
	if v.Transport != nil {
		craneOpts = append(craneOpts, crane.WithTransport(v.Transport))
	}

	if _, err := crane.Manifest(attRef, craneOpts...); err != nil {
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			logrus.Warnf("No attestation found for %s, skipping verification", ref)

			result.Verified = true

			return result, nil
		}

		return nil, fmt.Errorf("checking attestation for %s: %w", ref, err)
	}

	// Attestation exists, verify it cryptographically.
	logrus.Infof("Verifying attestation for %s", ref)

	err = v.verify(ctx, ref, v.identity(), v.issuer())
	if err == nil {
		result.Verified = true

		logrus.Infof("Attestation verified for %s", ref)

		return result, nil
	}

	if !isNoMatch(err) {
		return nil, fmt.Errorf("verifying attestation for %s: %w", ref, err)
	}

	// Nothing matched the configured identities or the predicate type.
	// Check whether the attestations verify under any identity.
	anyID := identityOptions{regexp: anyIdentity}

	anyErr := v.verify(ctx, ref, anyID, anyID)
	if anyErr == nil || isPredicateMismatch(anyErr) {
		logrus.Warnf(
			"Attestation of %s is not from a configured identity or predicate type, ignoring: %v",
			ref, err,
		)

		result.Verified = true
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("attestation of %s not matched: %v", ref, err))

		return result, nil
	}

	result.Verified = false
	result.Errors = append(result.Errors,
		fmt.Sprintf("attestation verification failed for %s: %v", ref, anyErr))

	return result, nil
}

// identityOptions is an exact value with its regular expression
// alternative. Cosign rejects a command that sets both.
type identityOptions struct {
	exact  string
	regexp string
}

// identity returns the certificate identity to verify against, preferring
// the regular expression.
func (v *CosignVerifier) identity() identityOptions {
	if v.CertIdentityRegexp != "" {
		return identityOptions{regexp: v.CertIdentityRegexp}
	}

	return identityOptions{exact: v.CertIdentity}
}

// issuer returns the OIDC issuer to verify against, preferring the regular
// expression.
func (v *CosignVerifier) issuer() identityOptions {
	if v.CertOidcIssuerRegexp != "" {
		return identityOptions{regexp: v.CertOidcIssuerRegexp}
	}

	return identityOptions{exact: v.CertOidcIssuer}
}

// predicateType returns the configured predicate type or the default.
func (v *CosignVerifier) predicateType() string {
	if v.PredicateType != "" {
		return v.PredicateType
	}

	return DefaultPredicateType
}

// verify runs a single verification pass through verifyFn.
func (v *CosignVerifier) verify(
	ctx context.Context, ref string, identity, issuer identityOptions,
) error {
	if v.verifyFn != nil {
		return v.verifyFn(ctx, ref, identity, issuer)
	}

	return v.verifyAttestation(ctx, ref, identity, issuer)
}

// verifyAttestation runs a single cosign verification pass.
func (v *CosignVerifier) verifyAttestation(
	ctx context.Context, ref string, identity, issuer identityOptions,
) error {
	cmd := v.verifyCommand(identity, issuer)

	if err := cmd.Exec(ctx, []string{ref}); err != nil {
		return fmt.Errorf("cosign verify-attestation %s: %w", ref, err)
	}

	return nil
}

// verifyCommand builds the cosign command for one verification pass.
// Cosign fails with KeyAndIdentityParseError when an exact identity and
// its regular expression are set at the same time, so identityOptions
// carries only one of them.
func (v *CosignVerifier) verifyCommand(
	identity, issuer identityOptions,
) cosignverify.VerifyAttestationCommand {
	cmd := cosignverify.VerifyAttestationCommand{
		CheckClaims:          true,
		IgnoreTlog:           false,
		PredicateType:        v.predicateType(),
		CertIdentity:         identity.exact,
		CertIdentityRegexp:   identity.regexp,
		CertOidcIssuer:       issuer.exact,
		CertOidcIssuerRegexp: issuer.regexp,
	}

	if v.Transport != nil {
		// Setting RegistryClientOpts replaces the whole default option
		// set of cosign, including its keychain, see
		// options.RegistryOptions.GetRegistryClientOpts.
		cmd.RegistryClientOpts = []ggcrremote.Option{
			ggcrremote.WithTransport(v.Transport),
			ggcrremote.WithAuthFromKeychain(gcrane.Keychain),
			ggcrremote.WithUserAgent(image.UserAgent),
		}
	}

	return cmd
}

// isNoMatch reports whether the verification failed because no attestation
// matched the requested identities or predicate type, rather than because
// an attestation failed to verify.
func isNoMatch(err error) bool {
	return isNoMatchingAttestations(err) || isPredicateMismatch(err)
}

// isNoMatchingAttestations reports whether cosign found no attestation for
// the requested identities. Cosign returns the same error for attestations
// signed by another identity and for attestations that do not verify.
func isNoMatchingAttestations(err error) bool {
	var noMatching *cosign.ErrNoMatchingAttestations

	return errors.As(err, &noMatching)
}

// isPredicateMismatch reports whether every attestation verified, but none
// of them carries the requested predicate type.
func isPredicateMismatch(err error) bool {
	if err == nil || isNoMatchingAttestations(err) {
		return false
	}

	return strings.Contains(err.Error(), predicateMismatchMessage)
}

// digestToAttestationTag converts a digest to the cosign attestation tag.
func digestToAttestationTag(dg image.Digest) string {
	return strings.ReplaceAll(string(dg), "sha256:", "sha256-") + attestationTagSuffix
}
