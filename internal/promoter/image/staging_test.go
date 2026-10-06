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

package imagepromoter

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/policylabs/attestation"
	"github.com/policylabs/collector/envelope/bundle"
	sdkoptions "github.com/sigstore/cosign/v2/cmd/cosign/cli/options"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/release-sdk/sign"
	"sigs.k8s.io/release-sdk/sign/signfakes"

	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	reg "sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	testTrustedPrincipal = "sigstore::" + testIssuer + "::" + testSignerIdentity
	testForeignPrincipal = "sigstore::" + testIssuer + "::" + testOtherIdentity
)

// testSubject is an in-toto subject with a digest.
type testSubject struct {
	digest map[string]string
}

func (s *testSubject) GetName() string              { return "" }
func (s *testSubject) GetUri() string               { return "" }
func (s *testSubject) GetDigest() map[string]string { return s.digest }

// testStatement is an in-toto statement about its subjects.
type testStatement struct {
	subjects []attestation.Subject
}

func (s *testStatement) GetSubjects() []attestation.Subject        { return s.subjects }
func (s *testStatement) GetPredicate() attestation.Predicate       { return nil } //nolint:ireturn // implements the attestation interface
func (s *testStatement) GetType() string                           { return "" }
func (s *testStatement) GetVerification() attestation.Verification { return nil } //nolint:ireturn // implements the attestation interface
func (s *testStatement) GetPredicateType() attestation.PredicateType {
	return provenance.SignaturePredicateType
}

// testEnvelope is an attestation envelope holding a statement about the
// given digests.
type testEnvelope struct {
	statement *testStatement
}

func newTestEnvelope(digests ...string) *testEnvelope {
	statement := &testStatement{}

	for _, digest := range digests {
		algorithm, value, _ := strings.Cut(digest, ":")
		statement.subjects = append(statement.subjects, &testSubject{
			digest: map[string]string{algorithm: value},
		})
	}

	return &testEnvelope{statement: statement}
}

func (e *testEnvelope) GetStatement() attestation.Statement       { return e.statement } //nolint:ireturn // implements the attestation interface
func (e *testEnvelope) GetPredicate() attestation.Predicate       { return nil }         //nolint:ireturn // implements the attestation interface
func (e *testEnvelope) GetSignatures() []attestation.Signature    { return nil }
func (e *testEnvelope) GetCertificate() attestation.Certificate   { return nil } //nolint:ireturn // implements the attestation interface
func (e *testEnvelope) GetVerification() attestation.Verification { return nil } //nolint:ireturn // implements the attestation interface
func (e *testEnvelope) Verify(...any) error                       { return nil }

// claimingBundle returns a sigstore bundle signature whose certificate
// names the identity. It does not verify.
func claimingBundle(t *testing.T, digest, identity string) provenance.Attestation {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	cert := testCertificate(t, key.Public(), identity)

	env := &bundle.Envelope{}
	env.VerificationMaterial = &protobundle.VerificationMaterial{
		Content: &protobundle.VerificationMaterial_Certificate{
			Certificate: &protocommon.X509Certificate{RawBytes: cert.Raw},
		},
	}

	att := signatureBundle(digest, provenance.SignatureFailed, digest)
	att.Envelope = env

	return att
}

// numberedDigest returns a valid, unique digest.
func numberedDigest(i int) string {
	return fmt.Sprintf("sha256:%064x", i)
}

// testStagingEdge returns an edge promoting a staging image to a tag.
func testStagingEdge(host, imageName, digest, tag string) promotion.Edge {
	return promotion.Edge{
		SrcRegistry: reg.Context{Name: image.Registry(host + "/staging"), Src: true},
		SrcImageTag: promotion.ImageTag{Name: image.Name(imageName)},
		Digest:      image.Digest(digest),
		DstRegistry: reg.Context{Name: image.Registry(host + "/production")},
		DstImageTag: promotion.ImageTag{Name: image.Name(imageName), Tag: image.Tag(tag)},
	}
}

// signatureBundle returns a discovered cosign bundle signature.
func signatureBundle(
	digest string, status provenance.SignatureStatus, subject string, signers ...string,
) provenance.Attestation {
	return provenance.Attestation{
		Digest:         digest,
		Source:         provenance.SourceReferrer,
		Location:       numberedDigest(999),
		PredicateType:  provenance.SignaturePredicateType,
		Status:         status,
		Signers:        signers,
		Envelope:       newTestEnvelope(subject),
		SubjectMatches: subject == digest,
	}
}

// locatedBundle returns a verified signature bundle of the foreign signer
// about the digest, at its own location.
func locatedBundle(digest, location string) provenance.Attestation {
	att := signatureBundle(digest, provenance.SignatureVerified, digest, testForeignPrincipal)
	att.Location = location

	return att
}

// fakeStagingSigner returns a release-sdk signer that reports the given
// references as signed, and fails to verify the invalid ones.
func fakeStagingSigner(signed map[string]bool, invalid map[string]bool) *sign.Signer {
	opts := sign.Default()
	// release-sdk verifies images in parallel goroutines sharing an error
	// variable, one worker keeps the race detector quiet.
	opts.MaxWorkers = 1

	impl := &signfakes.FakeImpl{}
	impl.ImagesSignedStub = func(_ context.Context, _ *sign.Signer, refs ...string) (*sync.Map, error) {
		res := &sync.Map{}
		for _, ref := range refs {
			res.Store(ref, signed[ref])
		}

		return res, nil
	}
	impl.VerifyImageInternalStub = func(
		_ context.Context, _ sdkoptions.CertVerifyOptions, _ string, refs []string, _ bool,
	) (*sign.SignedObject, error) {
		if invalid[refs[0]] {
			return nil, errors.New("no matching signatures")
		}

		return &sign.SignedObject{}, nil
	}
	impl.ParseReferenceStub = name.ParseReference
	impl.DigestStub = func(ref string, _ ...crane.Option) (string, error) {
		_, digest, _ := strings.Cut(ref, "@")

		return digest, nil
	}

	signer := sign.New(opts)
	signer.SetImpl(impl)

	return signer
}

func TestValidateStagingSignatures(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	legacyDigest := pushTestImage(t, di, host+"/staging/legacy:v1.0")
	pushTestSignature(t, di, host+"/staging/legacy", legacyDigest, "example.com/legacy",
		testSignerIdentity, testOtherIdentity)

	bundleDigest := numberedDigest(1)
	foreignDigest := numberedDigest(2)
	unsignedDigest := numberedDigest(3)
	undiscoveredDigest := numberedDigest(4)
	indexDigest := numberedDigest(5)
	policyDigest := numberedDigest(7)
	partialDigest := numberedDigest(8)

	edges := map[promotion.Edge]any{
		testStagingEdge(host, "legacy", legacyDigest, "v1.0"):           nil,
		testStagingEdge(host, "legacy", legacyDigest, "v1.1"):           nil,
		testStagingEdge(host, "bundle", bundleDigest, "v1.0"):           nil,
		testStagingEdge(host, "foreign", foreignDigest, "v1.0"):         nil,
		testStagingEdge(host, "unsigned", unsignedDigest, "v1.0"):       nil,
		testStagingEdge(host, "undiscovered", undiscoveredDigest, "v1"): nil,
		testStagingEdge(host, "index", indexDigest, "v1.0"):             nil,
		testStagingEdge(host, "policy", policyDigest, "v1.0"):           nil,
		testStagingEdge(host, "partial", partialDigest, "v1.0"):         nil,
	}

	ref := func(imageName, digest string) string {
		return host + "/staging/" + imageName + "@" + digest
	}

	di.signer = fakeStagingSigner(map[string]bool{ref("legacy", legacyDigest): true}, nil)

	discoveries := map[string]*provenance.Discovery{
		ref("legacy", legacyDigest): {},
		ref("bundle", bundleDigest): {Attestations: []provenance.Attestation{
			signatureBundle(bundleDigest, provenance.SignatureVerified, bundleDigest,
				testTrustedPrincipal, testForeignPrincipal),
			// Other predicate types are no signatures.
			{
				Digest:        bundleDigest,
				PredicateType: provenance.DefaultPredicateType,
				Status:        provenance.SignatureFailed,
			},
		}},
		ref("foreign", foreignDigest): {Attestations: []provenance.Attestation{
			signatureBundle(foreignDigest, provenance.SignatureVerified, foreignDigest, testForeignPrincipal),
		}},
		ref("unsigned", unsignedDigest): {},
		ref("index", indexDigest): {Attestations: []provenance.Attestation{
			// A failing signature of a child is not one of the index.
			signatureBundle(numberedDigest(6), provenance.SignatureFailed, numberedDigest(6)),
		}},
		ref("policy", policyDigest): {Attestations: []provenance.Attestation{
			locatedBundle(policyDigest, numberedDigest(11)),
		}},
		ref("partial", partialDigest): {Attestations: []provenance.Attestation{
			locatedBundle(partialDigest, numberedDigest(12)),
		}},
	}

	// Only bundles that all policies of the image accepted count. The
	// foreign image satisfies its policy without accepting its bundle,
	// the partial one fails one of its two policies.
	accepted := func(imageName, digest string) *provenance.PolicyResult {
		return &provenance.PolicyResult{
			Satisfied: true,
			Accepted:  []*provenance.Attestation{&discoveries[ref(imageName, digest)].Attestations[0]},
		}
	}
	outcomes := map[string]*provenance.ImageProvenance{
		ref("policy", policyDigest): {Results: []*provenance.PolicyResult{accepted("policy", policyDigest)}},
		ref("foreign", foreignDigest): {Results: []*provenance.PolicyResult{{
			Satisfied: true,
			Accepted:  []*provenance.Attestation{new(locatedBundle(foreignDigest, numberedDigest(13)))},
		}}},
		ref("partial", partialDigest): {Results: []*provenance.PolicyResult{
			accepted("partial", partialDigest), {Satisfied: false},
		}},
	}

	results, err := di.ValidateStagingSignatures(context.Background(), testSignCheckOptions(), edges, discoveries, outcomes)
	require.NoError(t, err)
	require.Len(t, results, 8)

	legacy := results[ref("legacy", legacyDigest)]
	require.Equal(t, provenance.SignatureVerified, legacy.Status)
	require.Equal(t, []string{testTrustedPrincipal}, legacy.Signers,
		"only the configured identity is a verified signer")
	require.Len(t, legacy.Edges, 2, "all edges of an image are kept")

	bundle := results[ref("bundle", bundleDigest)]
	require.Equal(t, provenance.SignatureVerified, bundle.Status)
	require.Equal(t, []string{testTrustedPrincipal}, bundle.Signers)

	foreign := results[ref("foreign", foreignDigest)]
	require.Equal(t, provenance.SignatureUntrusted, foreign.Status)
	require.Equal(t, []string{testForeignPrincipal}, foreign.Signers)
	require.Empty(t, foreign.PolicySigners)

	policy := results[ref("policy", policyDigest)]
	require.Equal(t, provenance.SignatureUntrusted, policy.Status, "policy signers don't change the status")
	require.Equal(t, []string{testForeignPrincipal}, policy.Signers)
	require.Equal(t, []string{testForeignPrincipal}, policy.PolicySigners)

	require.Empty(t, results[ref("partial", partialDigest)].PolicySigners)

	require.Equal(t, provenance.SignatureUnsigned, results[ref("unsigned", unsignedDigest)].Status)
	require.Equal(t, provenance.SignatureUnsigned, results[ref("index", indexDigest)].Status)

	undiscovered := results[ref("undiscovered", undiscoveredDigest)]
	require.Equal(t, provenance.SignatureUnverifiable, undiscovered.Status)
	require.NotEmpty(t, undiscovered.Errors)
}

func TestValidateStagingSignaturesInvalid(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)
	validDigest := pushTestImage(t, di, host+"/staging/valid:v1.0")
	pushTestSignature(t, di, host+"/staging/valid", validDigest, "example.com/valid", testSignerIdentity)

	invalidDigest := numberedDigest(2)
	bundleDigest := numberedDigest(3)

	validRef := host + "/staging/valid@" + validDigest
	invalidRef := host + "/staging/invalid@" + invalidDigest
	bundleRef := host + "/staging/bundle@" + bundleDigest

	edges := map[promotion.Edge]any{
		testStagingEdge(host, "valid", validDigest, "v1.0"):     nil,
		testStagingEdge(host, "invalid", invalidDigest, "v1.0"): nil,
		testStagingEdge(host, "bundle", bundleDigest, "v1.0"):   nil,
	}

	di.signer = fakeStagingSigner(
		map[string]bool{validRef: true, invalidRef: true},
		map[string]bool{invalidRef: true},
	)

	discoveries := map[string]*provenance.Discovery{
		validRef:   {},
		invalidRef: {},
		bundleRef: {Attestations: []provenance.Attestation{
			// A failed signature is not hidden by a verified one.
			signatureBundle(bundleDigest, provenance.SignatureVerified, bundleDigest, testTrustedPrincipal),
			claimingBundle(t, bundleDigest, testSignerIdentity),
		}},
	}

	results, err := di.ValidateStagingSignatures(context.Background(), testSignCheckOptions(), edges, discoveries, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), invalidRef)
	require.Contains(t, err.Error(), bundleRef)
	require.NotContains(t, err.Error(), validRef)

	// The valid image is still verified after the batch failed.
	require.Equal(t, provenance.SignatureVerified, results[validRef].Status)
	require.Equal(t, provenance.SignatureFailed, results[invalidRef].Status)
	require.Equal(t, provenance.SignatureFailed, results[bundleRef].Status)
	require.Empty(t, results[bundleRef].Signers)
}

func TestValidateStagingSignaturesUnknownSigner(t *testing.T) {
	t.Parallel()

	host, di := newTLSTestRegistry(t)

	// release-sdk verified the tag, but none of its signatures names the
	// configured identity.
	digest := pushTestImage(t, di, host+"/staging/other:v1.0")
	pushTestSignature(t, di, host+"/staging/other", digest, "example.com/other", testOtherIdentity)

	ref := host + "/staging/other@" + digest
	di.signer = fakeStagingSigner(map[string]bool{ref: true}, nil)

	results, err := di.ValidateStagingSignatures(context.Background(), testSignCheckOptions(),
		map[promotion.Edge]any{testStagingEdge(host, "other", digest, "v1.0"): nil}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, provenance.SignatureUnverifiable, results[ref].Status)
	require.Empty(t, results[ref].Signers)
}

func TestValidateStagingSignaturesWithoutDiscovery(t *testing.T) {
	t.Parallel()

	host := "registry.example.com"
	digest := numberedDigest(1)
	edges := map[promotion.Edge]any{
		testStagingEdge(host, "image", digest, "v1.0"): nil,
		// Edges without a source reference are skipped.
		{}: nil,
	}

	di := &DefaultPromoterImplementation{signer: fakeStagingSigner(nil, nil)}

	results, err := di.ValidateStagingSignatures(context.Background(), testSignCheckOptions(), edges, nil, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, provenance.SignatureUnsigned, results[host+"/staging/image@"+digest].Status)
}

func TestCheckSignatureBundle(t *testing.T) {
	t.Parallel()

	identity, err := signCheckIdentity(testSignCheckOptions())
	require.NoError(t, err)

	digest := numberedDigest(1)

	for _, tc := range []struct {
		name    string
		att     provenance.Attestation
		status  provenance.SignatureStatus
		signers []string
	}{
		{
			name:    "trusted",
			att:     signatureBundle(digest, provenance.SignatureVerified, digest, testTrustedPrincipal),
			status:  provenance.SignatureVerified,
			signers: []string{testTrustedPrincipal},
		},
		{
			name:    "foreign",
			att:     signatureBundle(digest, provenance.SignatureVerified, digest, testForeignPrincipal),
			status:  provenance.SignatureUntrusted,
			signers: []string{testForeignPrincipal},
		},
		{
			name:   "other subject",
			att:    signatureBundle(digest, provenance.SignatureVerified, numberedDigest(2), testTrustedPrincipal),
			status: provenance.SignatureFailed,
		},
		{
			name:   "other subject, foreign signer",
			att:    signatureBundle(digest, provenance.SignatureVerified, numberedDigest(2), testForeignPrincipal),
			status: provenance.SignatureUnsigned,
		},
		{
			name: "no statement",
			att: provenance.Attestation{
				Digest:  digest,
				Status:  provenance.SignatureVerified,
				Signers: []string{testTrustedPrincipal},
			},
			status: provenance.SignatureFailed,
		},
		{
			name:   "failed, claiming the configured identity",
			att:    claimingBundle(t, digest, testSignerIdentity),
			status: provenance.SignatureFailed,
		},
		{
			// For example signed with a key, or under another sigstore
			// instance.
			name:   "failed, claiming another identity",
			att:    claimingBundle(t, digest, testOtherIdentity),
			status: provenance.SignatureUnverifiable,
		},
		{
			name:   "failed, without certificate",
			att:    signatureBundle(digest, provenance.SignatureFailed, digest),
			status: provenance.SignatureUnverifiable,
		},
		{
			name:   "unverifiable",
			att:    signatureBundle(digest, provenance.SignatureUnverifiable, digest),
			status: provenance.SignatureUnverifiable,
		},
		{
			name:   "unsigned",
			att:    signatureBundle(digest, provenance.SignatureUnsigned, digest),
			status: provenance.SignatureUnsigned,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			check := checkSignatureBundle(identity, &tc.att, digest)
			require.Equal(t, tc.status, check.status)
			require.Equal(t, tc.signers, check.signers)
		})
	}
}

func TestMatchesPrincipal(t *testing.T) {
	t.Parallel()

	exact, err := signCheckIdentity(testSignCheckOptions())
	require.NoError(t, err)

	opts := testSignCheckOptions()
	opts.SignCheckIdentityRegexp = "(krel-staging|krel-trust)@k8s-releng-prod.iam.gserviceaccount.com"
	regexp, err := signCheckIdentity(opts)
	require.NoError(t, err)

	require.True(t, matchesPrincipal(exact, testTrustedPrincipal))
	require.True(t, matchesPrincipal(regexp, testTrustedPrincipal))
	require.False(t, matchesPrincipal(exact, testForeignPrincipal))
	require.False(t, matchesPrincipal(regexp, testForeignPrincipal))
	require.False(t, matchesPrincipal(exact, "sigstore::https://token.actions.githubusercontent.com::"+testSignerIdentity),
		"the issuer must match")
	require.False(t, matchesPrincipal(exact, "key::ecdsa::abc"))
	require.False(t, matchesPrincipal(exact, "invalid"))
}

func TestApplySignatureChecks(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		checks  []signatureCheck
		status  provenance.SignatureStatus
		signers []string
		errors  int
	}{
		{
			name:   "none",
			status: provenance.SignatureUnsigned,
		},
		{
			name: "failed wins over verified",
			checks: []signatureCheck{
				{status: provenance.SignatureVerified, signers: []string{"a"}},
				{status: provenance.SignatureFailed, err: "bad"},
			},
			status: provenance.SignatureFailed,
			errors: 1,
		},
		{
			name: "verified wins over untrusted",
			checks: []signatureCheck{
				{status: provenance.SignatureUntrusted, signers: []string{"b"}},
				{status: provenance.SignatureVerified, signers: []string{"a"}},
				{status: provenance.SignatureVerified, signers: []string{"a", "c"}},
			},
			status:  provenance.SignatureVerified,
			signers: []string{"a", "c"},
		},
		{
			name: "untrusted wins over unverifiable",
			checks: []signatureCheck{
				{status: provenance.SignatureUnverifiable, err: "no discovery"},
				{status: provenance.SignatureUntrusted, signers: []string{"b"}},
			},
			status:  provenance.SignatureUntrusted,
			signers: []string{"b"},
			errors:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := &promotion.StagingSignature{}
			applySignatureChecks(res, tc.checks)
			require.Equal(t, tc.status, res.Status)
			require.Equal(t, tc.signers, res.Signers)
			require.Len(t, res.Errors, tc.errors)
		})
	}
}
