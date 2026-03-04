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
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	cosignverify "github.com/sigstore/cosign/v2/cmd/cosign/cli/verify"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

const attestationTagSuffix = ".att"

// CosignVerifier verifies provenance attestations attached to container images
// using the cosign attestation tag convention.
//
// It uses verify-if-present semantics: when an attestation tag exists it
// is cryptographically verified using cosign; when no attestation is
// found a warning is logged and the image is still allowed through.
type CosignVerifier struct {
	// CertIdentity is the expected certificate identity for attestation
	// verification (e.g., "krel-trust@k8s-releng-prod.iam.gserviceaccount.com").
	CertIdentity string

	// CertIdentityRegexp is a regex alternative to CertIdentity.
	CertIdentityRegexp string

	// CertOidcIssuer is the expected OIDC issuer for the signing identity
	// (e.g., "https://accounts.google.com").
	CertOidcIssuer string

	// CertOidcIssuerRegexp is a regex alternative to CertOidcIssuer.
	CertOidcIssuerRegexp string
}

// Verify checks whether the image has a valid provenance attestation attached.
// It first checks for the attestation tag existence, then verifies the
// attestation signature using cosign.
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
	attRef := fmt.Sprintf("%s/%s:%s",
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

	_, err = crane.Manifest(attRef, craneOpts...)
	if err != nil {
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			logrus.Warnf("No attestation found for %s, skipping verification", ref)

			result.Verified = true

			return result, nil
		}

		return nil, fmt.Errorf("checking attestation for %s: %w", ref, err)
	}

	// Attestation exists — verify it cryptographically.
	logrus.Infof("Verifying attestation for %s", ref)

	cmd := cosignverify.VerifyAttestationCommand{
		CheckClaims: true,
		IgnoreTlog:  false,
	}

	cmd.CertIdentity = v.CertIdentity
	cmd.CertIdentityRegexp = v.CertIdentityRegexp
	cmd.CertOidcIssuer = v.CertOidcIssuer
	cmd.CertOidcIssuerRegexp = v.CertOidcIssuerRegexp

	if err := cmd.Exec(ctx, []string{ref}); err != nil {
		result.Verified = false
		result.Errors = append(result.Errors,
			fmt.Sprintf("attestation verification failed for %s: %v", ref, err))

		return result, nil
	}

	result.Verified = true

	logrus.Infof("Attestation verified for %s", ref)

	return result, nil
}

// digestToAttestationTag converts a digest to the cosign attestation tag.
func digestToAttestationTag(dg image.Digest) string {
	return strings.ReplaceAll(string(dg), "sha256:", "sha256-") + attestationTagSuffix
}
