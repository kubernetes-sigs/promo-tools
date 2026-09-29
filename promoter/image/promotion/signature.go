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

package promotion

import (
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
)

// StagingSignature is the result of checking the signatures of one
// staging image, both legacy cosign `.sig` tags and sigstore bundles.
type StagingSignature struct {
	// Reference is the source digest reference of the image.
	Reference string

	// Edges are all the promotion edges of the image.
	Edges []Edge

	// Status is the overall outcome. It is failed if a legacy signature
	// tag failed to verify, including one of another identity, or a
	// sigstore bundle claiming a configured identity did; verified if a
	// signature of a configured identity verified; untrusted if only
	// bundles of other identities verified; unverifiable if a signature
	// could not be checked; and unsigned if there is no signature.
	Status provenance.SignatureStatus

	// Signers are the signer principals behind Status: the configured
	// identities for verified, the other identities for untrusted.
	Signers []string

	// Errors explain why signatures failed or could not be checked.
	Errors []string
}

// StagingSignatures are the staging signature results by source digest
// reference.
type StagingSignatures map[string]*StagingSignature
