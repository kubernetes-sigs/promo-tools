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

	"github.com/carabiner-dev/attestation"
)

// Discoverer finds the attestations attached to a staging image.
//
//counterfeiter:generate . Discoverer
type Discoverer interface {
	// Discover returns every attestation found for the image at the
	// given digest reference (e.g., "gcr.io/staging/image@sha256:abc...").
	// An error means discovery could not complete; an image without
	// attestations returns an empty Discovery and no error.
	Discover(ctx context.Context, ref string) (*Discovery, error)
}

// Source says where an attestation was found.
type Source string

const (
	// SourceReferrer is an OCI referrer holding a sigstore bundle.
	SourceReferrer Source = "referrer"

	// SourceAttestationTag is a layer of a legacy cosign `.att` tag.
	SourceAttestationTag Source = "att-tag"
)

// SignatureStatus is the outcome of the cryptographic verification of an
// attestation or signature, independent of who signed it.
type SignatureStatus string

const (
	// SignatureVerified means the signature verified.
	SignatureVerified SignatureStatus = "verified"

	// SignatureUnsigned means there is no signature.
	SignatureUnsigned SignatureStatus = "unsigned"

	// SignatureUnverifiable means there is a signature, but nothing to
	// check it against (for example a key-signed DSSE envelope).
	SignatureUnverifiable SignatureStatus = "unverifiable"

	// SignatureFailed means the signature was checked and did not verify.
	SignatureFailed SignatureStatus = "failed"
)

// Discovery lists what was found for one image.
type Discovery struct {
	// Reference is the digest reference that was inspected.
	Reference string

	// Attestations are the attestations found on the image and, for an
	// index, on its children in the same repository.
	Attestations []Attestation

	// Referrers are the referrers that hold no attestation, for example
	// artifacts attached with another artifact type.
	Referrers []Referrer

	// Errors describe referrers and layers that could not be read or
	// parsed. They are skipped, so one malformed artifact does not hide
	// the attestations next to it. The DSSE layers of an `.att` tag are
	// read together and skipped together.
	Errors []string
}

// Attestation is one attestation found for an image.
type Attestation struct {
	// Digest is the digest the attestation is attached to: the image
	// itself or a child of an index.
	Digest string

	// Source says whether the attestation came from a referrer or an
	// `.att` tag.
	Source Source

	// Location is the digest of the referrer manifest, or the `.att` tag.
	Location string

	// Layer is the digest of the referrer layer that holds the envelope.
	// It is empty for `.att` tags.
	Layer string

	// PredicateType is the predicate type of the statement.
	PredicateType string

	// Status is the outcome of the signature verification.
	Status SignatureStatus

	// Signers are the verified signer identities, written as signer
	// principals (e.g., "sigstore::https://accounts.google.com::user@example.com").
	Signers []string

	// Error explains why the signature did not verify, if it did not.
	Error string

	// SubjectMatches is true when a subject of the statement has the
	// digest the attestation is attached to. A verified signature says who
	// signed the statement, not that it is about this image: an
	// attestation copied from another image verifies as well.
	SubjectMatches bool

	// Envelope is the parsed attestation, for policy evaluation.
	Envelope attestation.Envelope
}

// Referrer is a referrer that holds no attestation.
type Referrer struct {
	// Digest is the digest the referrer is attached to.
	Digest string

	// Location is the digest of the referrer manifest.
	Location string

	// ArtifactType is the artifact type of the referrer.
	ArtifactType string
}
