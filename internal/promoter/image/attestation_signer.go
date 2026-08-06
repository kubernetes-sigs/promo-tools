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
	"bytes"
	"fmt"

	signer "github.com/carabiner-dev/signer"
	"github.com/sigstore/sigstore/pkg/oauthflow"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
)

// statementSigner signs an in-toto statement and returns a sigstore bundle
// that can be attached to an image.
type statementSigner interface {
	SignStatement(statement []byte) ([]byte, error)
}

// carabinerSigner implements statementSigner using the carabiner-dev
// signer with sigstore keyless signing. The underlying signer is safe
// for concurrent use, so signing parallelizes up to MaxSignatureOps.
// The Fulcio cert is fetched once and reused by every signature  until
// it expires.
type carabinerSigner struct {
	signer *signer.Signer
}

func (cs *carabinerSigner) SignStatement(statement []byte) ([]byte, error) {
	bndl, err := cs.signer.SignStatementBundle(statement)
	if err != nil {
		return nil, fmt.Errorf("signing statement: %w", err)
	}

	var buf bytes.Buffer
	if err := cs.signer.WriteBundle(bndl, &buf); err != nil {
		return nil, fmt.Errorf("serializing bundle: %w", err)
	}

	return buf.Bytes(), nil
}

// ensureAttestationSigner initializes the attestation signer if it is not
// already set. The signing token comes from the promoter's identity token
// provider for --signer-account which is the same identity that signs the
// promoted images. This controls that attestations are only signed with the
// token and never falling back unintentionally to ambient credentials, etc.
func (di *DefaultPromoterImplementation) ensureAttestationSigner(opts *options.Options) error {
	if di.attSigner != nil {
		return nil
	}

	token, err := di.GetIdentityToken(opts, opts.SignerAccount)
	if err != nil {
		return fmt.Errorf("getting attestation signing token: %w", err)
	}

	s := signer.NewSigner()

	// Inject the token and disable the signer's ambient STS discovery
	// so no other identity source (CI tokens, interactive flows) can ever
	// sign a promotion attestation.
	s.Options.Token = &oauthflow.OIDCIDToken{RawString: token}
	s.Options.DisableSTS = true

	di.attSigner = &carabinerSigner{signer: s}

	return nil
}
