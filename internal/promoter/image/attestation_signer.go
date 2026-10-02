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

	s, err := di.newStatementSigner(opts, opts.SignerAccount)
	if err != nil {
		return err
	}

	di.attSigner = s

	return nil
}

// ensureSummarySigner initializes the signer of the verification summaries
// if it is not already set. It is the attestation signer, unless
// --summary-signer-account names another identity, which then is the only
// one that signs them.
func (di *DefaultPromoterImplementation) ensureSummarySigner(opts *options.Options) error {
	if di.summarySigner != nil {
		return nil
	}

	account := summarySignerAccount(opts)
	if account == opts.SignerAccount {
		if err := di.ensureAttestationSigner(opts); err != nil {
			return err
		}

		di.summarySigner = di.attSigner

		return nil
	}

	s, err := di.newStatementSigner(opts, account)
	if err != nil {
		return err
	}

	di.summarySigner = s

	return nil
}

// summarySignerAccount returns the service account whose identity signs the
// verification summaries, --summary-signer-account or else --signer-account.
func summarySignerAccount(opts *options.Options) string {
	if opts.SummarySignerAccount != "" {
		return opts.SummarySignerAccount
	}

	return opts.SignerAccount
}

// newStatementSigner returns a keyless signer for the identity of the
// service account.
func (di *DefaultPromoterImplementation) newStatementSigner(
	opts *options.Options, account string,
) (*carabinerSigner, error) {
	token, err := di.GetIdentityToken(opts, account)
	if err != nil {
		return nil, fmt.Errorf("getting signing token for %s: %w", account, err)
	}

	s := signer.NewSigner()

	// Inject the token and disable the signer's ambient STS discovery
	// so no other identity source (CI tokens, interactive flows) can ever
	// sign an attestation or summary of the promoter.
	s.Options.Token = &oauthflow.OIDCIDToken{RawString: token}
	s.Options.DisableSTS = true

	return &carabinerSigner{signer: s}, nil
}
