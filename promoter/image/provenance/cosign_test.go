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
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	testIdentity = "krel-trust@k8s-releng-prod.iam.gserviceaccount.com"
	testIssuer   = "https://accounts.google.com"
	testRegexp   = "(krel-staging|krel-trust)@.*"
)

// pushTestImage pushes a random image and returns its digest reference.
func pushTestImage(t *testing.T, repo string) string {
	t.Helper()

	img, err := random.Image(256, 1)
	require.NoError(t, err)
	require.NoError(t, crane.Push(img, repo+":latest"))

	digest, err := crane.Digest(repo + ":latest")
	require.NoError(t, err)

	return repo + "@" + digest
}

// newTestRepo starts an in-process registry and returns a repository path.
func newTestRepo(t *testing.T) string {
	t.Helper()

	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)

	return s.Listener.Addr().String() + "/test/image"
}

// attestationTagFor returns the cosign attestation tag reference for a
// digest reference.
func attestationTagFor(t *testing.T, repo, digestRef string) string {
	t.Helper()

	_, dg, ok := strings.Cut(digestRef, "@")
	require.True(t, ok)

	return repo + ":" + digestToAttestationTag(image.Digest(dg))
}

func TestVerifyNoAttestation(t *testing.T) {
	repo := newTestRepo(t)
	ref := pushTestImage(t, repo)

	called := false
	v := &CosignVerifier{
		CertIdentity:   testIdentity,
		CertOidcIssuer: testIssuer,
		verifyFn: func(context.Context, string, identityOptions, identityOptions) error {
			called = true

			return nil
		},
	}

	result, err := v.Verify(context.Background(), ref)
	require.NoError(t, err)
	require.True(t, result.Verified)
	require.False(t, called, "no attestation tag, cosign must not run")
}

func TestVerifyRequiresDigest(t *testing.T) {
	v := &CosignVerifier{}

	_, err := v.Verify(context.Background(), "gcr.io/foo/bar:v1.0")
	require.ErrorContains(t, err, "must include a digest")
}

func TestVerifyWithAttestation(t *testing.T) {
	// A cosign.ErrNoMatchingAttestations cannot be constructed outside of
	// cosign (its cause is unexported and formatting an empty one panics),
	// so the flow below uses the predicate mismatch error, which takes the
	// same branch. TestIsNoMatch covers the typed error.
	noMatch := fmt.Errorf(
		"%s: %s, found: other", predicateMismatchMessage, DefaultPredicateType,
	)

	for _, tc := range []struct {
		name string
		// errs are returned by consecutive verifyFn calls.
		errs         []error
		wantVerified bool
		wantWarnings bool
		wantErr      bool
		wantCalls    int
	}{
		{
			name:         "trusted identity verifies",
			errs:         []error{nil},
			wantVerified: true,
			wantCalls:    1,
		},
		{
			name: "attestation not matching the configured identity is ignored",
			// The configured pass finds nothing, the any-identity pass
			// verifies: the attestation is valid, just not ours.
			errs:         []error{noMatch, nil},
			wantVerified: true,
			wantWarnings: true,
			wantCalls:    2,
		},
		{
			name:         "attestation with another predicate type is ignored",
			errs:         []error{noMatch, noMatch},
			wantVerified: true,
			wantWarnings: true,
			wantCalls:    2,
		},
		{
			// Nothing verifies under any identity, so the attestation is
			// malformed or tampered with. An attestation signed with a
			// key rather than keyless ends up here too, until per-project
			// policy can express that.
			name:         "attestation that does not verify blocks promotion",
			errs:         []error{noMatch, errors.New("invalid signature")},
			wantVerified: false,
			wantCalls:    2,
		},
		{
			name:      "unexpected failures are surfaced",
			errs:      []error{errors.New("registry unreachable")},
			wantErr:   true,
			wantCalls: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepo(t)
			ref := pushTestImage(t, repo)

			// Attach something at the cosign attestation tag so the
			// existence check passes.
			attImg, err := random.Image(256, 1)
			require.NoError(t, err)
			require.NoError(t, crane.Push(attImg, attestationTagFor(t, repo, ref)))

			var calls []identityOptions

			v := &CosignVerifier{
				CertIdentityRegexp:   testRegexp,
				CertOidcIssuerRegexp: testIssuer,
				verifyFn: func(_ context.Context, _ string, identity, _ identityOptions) error {
					calls = append(calls, identity)

					return tc.errs[len(calls)-1]
				},
			}

			result, err := v.Verify(context.Background(), ref)

			require.Len(t, calls, tc.wantCalls)
			require.Equal(t, testRegexp, calls[0].regexp, "first pass uses the configured identity")

			if tc.wantCalls > 1 {
				require.Equal(t, anyIdentity, calls[1].regexp, "second pass accepts any identity")
			}

			if tc.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.wantVerified, result.Verified)

			if tc.wantVerified {
				require.Empty(t, result.Errors)
			} else {
				require.NotEmpty(t, result.Errors)
			}

			if tc.wantWarnings {
				require.NotEmpty(t, result.Warnings)
			}
		})
	}
}

func TestIdentityOptionsAreMutuallyExclusive(t *testing.T) {
	// Cosign fails with KeyAndIdentityParseError when an exact identity
	// and its regular expression are both set. SignCheckIdentity has a
	// default, so production runs always set both.
	v := &CosignVerifier{
		CertIdentity:         testIdentity,
		CertIdentityRegexp:   testRegexp,
		CertOidcIssuer:       testIssuer,
		CertOidcIssuerRegexp: ".*",
	}

	require.Equal(t, identityOptions{regexp: testRegexp}, v.identity())
	require.Equal(t, identityOptions{regexp: ".*"}, v.issuer())

	exact := &CosignVerifier{CertIdentity: testIdentity, CertOidcIssuer: testIssuer}
	require.Equal(t, identityOptions{exact: testIdentity}, exact.identity())
	require.Equal(t, identityOptions{exact: testIssuer}, exact.issuer())
}

func TestPredicateType(t *testing.T) {
	// Cosign rejects an empty predicate type with "missing predicate type",
	// which blocked every image carrying an attestation.
	require.Equal(t, DefaultPredicateType, (&CosignVerifier{}).predicateType())
	require.NotEmpty(t, DefaultPredicateType)

	const custom = "https://example.org/predicate/v1"
	require.Equal(t, custom, (&CosignVerifier{PredicateType: custom}).predicateType())
}

// TestVerifyCommand checks the cosign command of a verification pass.
// Production sets both SignCheckIdentity (which has a default) and
// SignCheckIdentityRegexp, and cosign rejects a command carrying both.
func TestVerifyCommand(t *testing.T) {
	v := &CosignVerifier{
		CertIdentity:         testIdentity,
		CertIdentityRegexp:   testRegexp,
		CertOidcIssuer:       testIssuer,
		CertOidcIssuerRegexp: ".*",
	}

	cmd := v.verifyCommand(v.identity(), v.issuer())

	require.Empty(t, cmd.CertIdentity)
	require.Equal(t, testRegexp, cmd.CertIdentityRegexp)
	require.Empty(t, cmd.CertOidcIssuer)
	require.Equal(t, ".*", cmd.CertOidcIssuerRegexp)
	require.Equal(t, DefaultPredicateType, cmd.PredicateType)
	require.True(t, cmd.CheckClaims)
	require.False(t, cmd.IgnoreTlog)
	require.Nil(t, cmd.RegistryClientOpts, "cosign keeps its own defaults without a transport")

	// Setting registry options replaces the cosign defaults, so the
	// keychain and user agent have to come along with the transport.
	withTransport := &CosignVerifier{Transport: http.DefaultTransport}
	cmd = withTransport.verifyCommand(identityOptions{}, identityOptions{})
	require.Len(t, cmd.RegistryClientOpts, 3)

	// Without a regular expression the exact values are used.
	exact := &CosignVerifier{CertIdentity: testIdentity, CertOidcIssuer: testIssuer}
	cmd = exact.verifyCommand(exact.identity(), exact.issuer())

	require.Equal(t, testIdentity, cmd.CertIdentity)
	require.Empty(t, cmd.CertIdentityRegexp)
	require.Equal(t, testIssuer, cmd.CertOidcIssuer)
	require.Empty(t, cmd.CertOidcIssuerRegexp)
}

func TestIsNoMatch(t *testing.T) {
	require.True(t, isNoMatch(&cosign.ErrNoMatchingAttestations{}))
	require.True(t, isNoMatch(fmt.Errorf("wrapped: %w", &cosign.ErrNoMatchingAttestations{})))
	require.True(t, isNoMatch(errors.New(predicateMismatchMessage+": x, found: y")))
	require.False(t, isNoMatch(errors.New("registry unreachable")))
	require.False(t, isNoMatch(nil))
}
