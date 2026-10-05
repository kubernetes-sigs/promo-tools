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
	"crypto/x509"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/policylabs/collector/envelope/bundle"
	sapi "github.com/policylabs/signer/api/v1"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sirupsen/logrus"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// stagingSignatureConcurrency is the number of staging images whose
// signatures are read in parallel.
const stagingSignatureConcurrency = 10

// statusRank orders signature outcomes: when an image has several
// signatures, the outcome with the highest rank is its status. A failed
// signature always wins, so it is never hidden by a verified one.
var statusRank = map[provenance.SignatureStatus]int{
	provenance.SignatureUnsigned:     0,
	provenance.SignatureUnverifiable: 1,
	provenance.SignatureUntrusted:    2,
	provenance.SignatureVerified:     3,
	provenance.SignatureFailed:       4,
}

// signatureTag is the outcome of verifying the legacy signature tag of an
// image with release-sdk.
type signatureTag struct {
	verified bool
	err      error
}

// signatureCheck is the outcome of checking one signature, or all
// signatures of one kind.
type signatureCheck struct {
	status  provenance.SignatureStatus
	signers []string
	err     string

	// accepted are the signers of an untrusted bundle that all
	// provenance policies of the image accepted.
	accepted []string
}

// ValidateStagingSignatures checks the signatures of the staging images of
// the edges and returns the result per source reference. Legacy signature
// tags are verified with release-sdk, sigstore bundle signatures are taken
// from the discoveries, keyed by source reference. Without discoveries,
// bundles are not checked; a reference missing from them failed discovery.
//
// Unsigned images and sigstore bundles signed only by other identities pass.
// Bundles of other identities that all provenance policies of the image
// accepted, from the outcomes keyed by source reference, are reported as
// accepted by the policies; they don't change the status.
// Legacy signature tags of other identities fail, because release-sdk can't
// tell them apart from invalid ones. It returns an error, along with the
// results, if a signature is invalid.
func (di *DefaultPromoterImplementation) ValidateStagingSignatures(
	ctx context.Context,
	opts *options.Options,
	edges map[promotion.Edge]any,
	discoveries map[string]*provenance.Discovery,
	outcomes map[string]*provenance.ImageProvenance,
) (promotion.StagingSignatures, error) {
	identity, err := signCheckIdentity(opts)
	if err != nil {
		return nil, err
	}

	results := stagingSignatureResults(edges)
	refs := slices.Sorted(maps.Keys(results))

	var wg sync.WaitGroup

	sem := make(chan struct{}, stagingSignatureConcurrency)

	for _, ref := range refs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			var checks []signatureCheck

			checks = append(checks, di.checkSignatureTag(ctx, identity, results[ref], di.verifySignatureTag(ref)))

			if discoveries != nil {
				checks = append(checks, checkSignatureBundles(identity, outcomes[ref], results[ref], discoveries)...)
			}

			applySignatureChecks(results[ref], checks)
		})
	}

	wg.Wait()

	var invalid []string

	for _, ref := range refs {
		logStagingSignature(results[ref])

		if results[ref].Status == provenance.SignatureFailed {
			invalid = append(invalid, ref)
		}
	}

	if len(invalid) > 0 {
		return results, fmt.Errorf("staging signatures failed to verify: %s", strings.Join(invalid, ", "))
	}

	return results, nil
}

// stagingSignatureResults returns an empty result for each source
// reference of the edges, holding all edges of that reference.
func stagingSignatureResults(edges map[promotion.Edge]any) promotion.StagingSignatures {
	results := promotion.StagingSignatures{}

	for edge := range edges {
		ref := edge.SrcReference()
		if ref == "" {
			continue
		}

		res, ok := results[ref]
		if !ok {
			res = &promotion.StagingSignature{
				Reference: ref,
				Status:    provenance.SignatureUnsigned,
			}
			results[ref] = res
		}

		res.Edges = append(res.Edges, edge)
	}

	for _, res := range results {
		slices.SortFunc(res.Edges, func(a, b promotion.Edge) int {
			return strings.Compare(a.DstReference()+":"+string(a.DstImageTag.Tag),
				b.DstReference()+":"+string(b.DstImageTag.Tag))
		})
	}

	return results
}

// verifySignatureTag verifies the legacy signature tag of one image with
// release-sdk. Verifying one image per call reports every invalid signature,
// and keeps release-sdk from verifying images in goroutines that share an
// error variable.
func (di *DefaultPromoterImplementation) verifySignatureTag(ref string) signatureTag {
	res, err := di.signer.VerifyImages(ref)
	if err != nil {
		return signatureTag{err: err}
	}

	_, verified := res.Load(ref)

	return signatureTag{verified: verified}
}

// checkSignatureTag turns the release-sdk outcome for the signature tag of
// an image into a check, reading the identities of the verified signature.
func (di *DefaultPromoterImplementation) checkSignatureTag(
	ctx context.Context, identity *verify.CertificateIdentity, res *promotion.StagingSignature, tag signatureTag,
) signatureCheck {
	switch {
	case tag.err != nil:
		return signatureCheck{
			status: provenance.SignatureFailed,
			err:    fmt.Sprintf("signature tag: %v", tag.err),
		}
	case !tag.verified:
		return signatureCheck{status: provenance.SignatureUnsigned}
	}

	signers, err := di.signatureTagSigners(ctx, identity, res.Reference)
	if err != nil {
		// The signature verified, only its signers are unknown.
		logrus.Warnf("Unable to read the signers of the staging signature of %s: %v", res.Reference, err)

		return signatureCheck{status: provenance.SignatureVerified}
	}

	if len(signers) == 0 {
		return signatureCheck{
			status: provenance.SignatureUnverifiable,
			err:    "signature tag: verified, but none of its signatures names a configured identity",
		}
	}

	return signatureCheck{status: provenance.SignatureVerified, signers: signers}
}

// signatureTagSigners returns the principals of the signatures in the
// signature tag of an image that have a configured identity and sign the
// image digest. release-sdk verified at least one of them.
func (di *DefaultPromoterImplementation) signatureTagSigners(
	ctx context.Context, identity *verify.CertificateIdentity, ref string,
) ([]string, error) {
	digest, err := name.NewDigest(ref)
	if err != nil {
		return nil, fmt.Errorf("parsing digest reference: %w", err)
	}

	sigRef := digest.Context().Tag(digestToSignatureTag(image.Digest(digest.DigestStr())))

	sigImage, err := remote.Image(sigRef, append(di.remoteOptions(), remote.WithContext(ctx))...)
	if err != nil {
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			return nil, nil
		}

		return nil, fmt.Errorf("reading signature %s: %w", sigRef, err)
	}

	manifest, err := sigImage.Manifest()
	if err != nil {
		return nil, fmt.Errorf("reading signature manifest %s: %w", sigRef, err)
	}

	var signers []string

	for i := range manifest.Layers {
		layer := &manifest.Layers[i]
		if layer.MediaType != simpleSigningMediaType {
			continue
		}

		certs, err := cryptoutils.UnmarshalCertificatesFromPEM(
			[]byte(layer.Annotations[certificateAnnotation]),
		)
		if err != nil || len(certs) == 0 || !matchesIdentity(identity, certs[0]) {
			continue
		}

		signed, ok, err := signaturePayload(sigImage, layer.Digest)
		if err != nil {
			return nil, fmt.Errorf("reading signature payload of %s: %w", sigRef, err)
		}

		if !ok || signed.Critical.Image.DockerManifestDigest != digest.DigestStr() {
			continue
		}

		summary, err := certificate.SummarizeCertificate(certs[0])
		if err != nil {
			continue
		}

		signers = append(signers, sigstorePrincipal(summary.Issuer, summary.SubjectAlternativeName))
	}

	return signers, nil
}

// checkSignatureBundles checks the sigstore bundle signatures of an image
// found by discovery. Signatures of index children are not signatures of
// the image, so only those of the image digest count.
func checkSignatureBundles(
	identity *verify.CertificateIdentity,
	outcome *provenance.ImageProvenance,
	res *promotion.StagingSignature,
	discoveries map[string]*provenance.Discovery,
) []signatureCheck {
	discovery, ok := discoveries[res.Reference]
	if !ok || discovery == nil {
		return []signatureCheck{{
			status: provenance.SignatureUnverifiable,
			err:    "attestation discovery failed, sigstore bundle signatures not checked",
		}}
	}

	digest := string(res.Edges[0].Digest)

	var checks []signatureCheck

	for i := range discovery.Attestations {
		att := &discovery.Attestations[i]
		if att.PredicateType != provenance.SignaturePredicateType || att.Digest != digest {
			continue
		}

		check := checkSignatureBundle(identity, att, digest)
		if check.status == provenance.SignatureUntrusted && outcome.AcceptedByAll(att) {
			check.accepted = check.signers
		}

		checks = append(checks, check)
	}

	return checks
}

// checkSignatureBundle checks one sigstore bundle signature of an image.
func checkSignatureBundle(
	identity *verify.CertificateIdentity, att *provenance.Attestation, digest string,
) signatureCheck {
	switch att.Status {
	case provenance.SignatureVerified:
	case provenance.SignatureUnsigned:
		return signatureCheck{status: provenance.SignatureUnsigned}
	case provenance.SignatureFailed:
		// Bundles signed with a key or under another sigstore instance
		// don't verify against the sigstore trust roots either. Only a
		// bundle that claims a configured identity is invalid.
		if !bundleClaimsIdentity(identity, att) {
			return signatureCheck{
				status: provenance.SignatureUnverifiable,
				err:    bundleError(att, "did not verify"),
			}
		}

		return signatureCheck{
			status: provenance.SignatureFailed,
			err:    bundleError(att, "did not verify"),
		}
	default:
		return signatureCheck{
			status: provenance.SignatureUnverifiable,
			err:    bundleError(att, "could not be verified"),
		}
	}

	var trusted, other []string

	for _, signer := range att.Signers {
		if matchesPrincipal(identity, signer) {
			trusted = append(trusted, signer)
		} else {
			other = append(other, signer)
		}
	}

	if !att.SubjectMatches {
		// A bundle of another identity about another image says nothing
		// about this one. One of a configured identity is misplaced.
		if len(trusted) == 0 {
			return signatureCheck{status: provenance.SignatureUnsigned}
		}

		return signatureCheck{
			status: provenance.SignatureFailed,
			err:    fmt.Sprintf("signature bundle %s does not sign %s", att.Location, digest),
		}
	}

	if len(trusted) > 0 {
		return signatureCheck{status: provenance.SignatureVerified, signers: trusted}
	}

	return signatureCheck{status: provenance.SignatureUntrusted, signers: other}
}

// bundleClaimsIdentity reports whether the signing certificate of a
// sigstore bundle names a configured identity.
func bundleClaimsIdentity(identity *verify.CertificateIdentity, att *provenance.Attestation) bool {
	env, ok := att.Envelope.(*bundle.Envelope)
	if !ok {
		return false
	}

	material := env.GetVerificationMaterial()

	raw := material.GetCertificate().GetRawBytes()
	if chain := material.GetX509CertificateChain().GetCertificates(); raw == nil && len(chain) > 0 {
		raw = chain[0].GetRawBytes()
	}

	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		return false
	}

	return matchesIdentity(identity, cert)
}

// bundleError describes why a signature bundle did not verify, falling
// back to the given reason when discovery recorded none.
func bundleError(att *provenance.Attestation, reason string) string {
	if att.Error != "" {
		reason = att.Error
	}

	return fmt.Sprintf("signature bundle %s: %s", att.Location, reason)
}

// matchesPrincipal reports whether a signer principal is a sigstore
// identity that matches the expected certificate identity.
func matchesPrincipal(identity *verify.CertificateIdentity, principal string) bool {
	id, err := sapi.NewIdentityFromPrincipal(principal)
	if err != nil || id.GetSigstore() == nil {
		return false
	}

	return identity.Verify(certificate.Summary{
		SubjectAlternativeName: id.GetSigstore().GetIdentity(),
		Issuer:                 id.GetSigstore().GetIssuer(),
	}) == nil
}

// sigstorePrincipal returns the signer principal of a sigstore identity.
func sigstorePrincipal(issuer, identity string) string {
	id := &sapi.Identity{Sigstore: &sapi.IdentitySigstore{Issuer: issuer, Identity: identity}}

	return id.Principal()
}

// applySignatureChecks sets the status of an image to the highest ranked
// outcome of its signature checks, with the signers of that outcome and
// the errors of all checks.
func applySignatureChecks(res *promotion.StagingSignature, checks []signatureCheck) {
	res.Status = provenance.SignatureUnsigned
	res.Signers = nil
	res.PolicySigners = nil
	res.Errors = nil

	for _, check := range checks {
		if statusRank[check.status] > statusRank[res.Status] {
			res.Status = check.status
		}

		if check.err != "" {
			res.Errors = append(res.Errors, check.err)
		}

		res.PolicySigners = append(res.PolicySigners, check.accepted...)
	}

	for _, check := range checks {
		if check.status == res.Status {
			res.Signers = append(res.Signers, check.signers...)
		}
	}

	slices.Sort(res.Signers)
	res.Signers = slices.Compact(res.Signers)
	slices.Sort(res.PolicySigners)
	res.PolicySigners = slices.Compact(res.PolicySigners)
}

// logStagingSignature logs the signature status of a staging image.
func logStagingSignature(res *promotion.StagingSignature) {
	switch res.Status {
	case provenance.SignatureVerified:
		logrus.Infof("Staging signature of %s verified: %s", res.Reference, joinOrUnknown(res.Signers))
	case provenance.SignatureUnsigned:
		logrus.Infof("Staging image %s is not signed", res.Reference)
	case provenance.SignatureUntrusted:
		if len(res.PolicySigners) > 0 {
			logrus.Infof(
				"Staging image %s is signed by %s, accepted by its provenance policies",
				res.Reference, strings.Join(res.PolicySigners, ", "),
			)

			return
		}

		logrus.Warnf(
			"Staging image %s is only signed by identities that are not configured: %s",
			res.Reference, joinOrUnknown(res.Signers),
		)
	case provenance.SignatureUnverifiable:
		logrus.Warnf("Staging signature of %s could not be verified: %s", res.Reference, strings.Join(res.Errors, "; "))
	case provenance.SignatureFailed:
		logrus.Errorf("Staging signature of %s is invalid: %s", res.Reference, strings.Join(res.Errors, "; "))
	}
}

// joinOrUnknown joins the signers, or says that they are unknown.
func joinOrUnknown(signers []string) string {
	if len(signers) == 0 {
		return "unknown signer"
	}

	return strings.Join(signers, ", ")
}
