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
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

const attestationTagSuffix = ".att"

// CosignVerifier checks for the existence of SLSA attestations
// attached to container images using the cosign attestation tag convention.
//
// Currently this only verifies that an attestation tag exists — it does
// not inspect the attestation contents or enforce Policy.AllowedBuilders
// / AllowedSourceRepos. Policy enforcement will be added in a follow-up.
type CosignVerifier struct{}

// Verify checks whether the image has a SLSA attestation attached.
// It looks for an attestation tag following the cosign convention
// (sha256-<hash>.att).
//
// Note: crane.Manifest does not accept a context, so cancellation is
// not propagated to the underlying HTTP call.
func (v *CosignVerifier) Verify(ctx context.Context, ref string) (*Result, error) {
	_ = ctx // crane.Manifest does not support context
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
	opts := []crane.Option{
		crane.WithAuthFromKeychain(gcrane.Keychain),
		crane.WithUserAgent(image.UserAgent),
	}

	_, err = crane.Manifest(attRef, opts...)
	if err != nil {
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			result.Verified = false
			result.Errors = append(result.Errors,
				"no attestation found for "+ref)
			return result, nil
		}
		return nil, fmt.Errorf("checking attestation for %s: %w", ref, err)
	}

	result.Verified = true
	logrus.Debugf("Attestation found for %s", ref)
	return result, nil
}

// digestToAttestationTag converts a digest to the cosign attestation tag.
func digestToAttestationTag(dg image.Digest) string {
	return strings.ReplaceAll(string(dg), "sha256:", "sha256-") + attestationTagSuffix
}
