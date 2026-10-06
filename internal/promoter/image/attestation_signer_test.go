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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/promoter/image/auth"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
)

func TestEnsureAttestationSigner(t *testing.T) {
	t.Parallel()

	opts := &options.Options{SignerAccount: TestSigningAccount}

	t.Run("token provider fails", func(t *testing.T) {
		t.Parallel()

		di := &DefaultPromoterImplementation{}
		di.SetIdentityTokenProvider(&auth.StaticIdentityTokenProvider{
			Err: errors.New("no credentials"),
		})

		// A failed token is fatal: there is no fallback identity source.
		err := di.ensureAttestationSigner(opts)
		require.Error(t, err)
		require.ErrorContains(t, err, "getting signing token")
		require.Nil(t, di.attSigner)
	})

	t.Run("token injected into signer", func(t *testing.T) {
		t.Parallel()

		di := &DefaultPromoterImplementation{}
		di.SetIdentityTokenProvider(&auth.StaticIdentityTokenProvider{
			Token: "test-token",
		})

		require.NoError(t, di.ensureAttestationSigner(opts))
		require.NotNil(t, di.attSigner)

		cs, ok := di.attSigner.(*policylabsSigner)
		require.True(t, ok)
		require.Equal(t, "test-token", cs.signer.Options.Token.RawString)
		require.True(t, cs.signer.Options.DisableSTS,
			"the injected token must be the signer's only credential source")
	})

	t.Run("injected signer is kept", func(t *testing.T) {
		t.Parallel()

		fake := &fakeStatementSigner{}
		di := &DefaultPromoterImplementation{attSigner: fake}

		require.NoError(t, di.ensureAttestationSigner(opts))
		require.Same(t, fake, di.attSigner, "an injected signer must not be replaced")
	})
}

// accountTokenProvider returns a token naming the service account it is
// requested for.
type accountTokenProvider struct{}

func (accountTokenProvider) GetIdentityToken(_ context.Context, serviceAccount, _ string) (string, error) {
	return "token-for-" + serviceAccount, nil
}

// signerToken returns the token a keyless signer signs with.
func signerToken(t *testing.T, s statementSigner) string {
	t.Helper()

	cs, ok := s.(*policylabsSigner)
	require.True(t, ok)
	require.True(t, cs.signer.Options.DisableSTS)

	return cs.signer.Options.Token.RawString
}

func TestEnsureSummarySigner(t *testing.T) {
	t.Parallel()

	const summaryAccount = "summaries@example.iam.gserviceaccount.com"

	for _, tc := range []struct {
		name    string
		account string
		want    string
		shared  bool
	}{
		{name: "defaults to the signer account", want: "token-for-" + TestSigningAccount, shared: true},
		{name: "same account", account: TestSigningAccount, want: "token-for-" + TestSigningAccount, shared: true},
		{name: "own account", account: summaryAccount, want: "token-for-" + summaryAccount},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			di := &DefaultPromoterImplementation{}
			di.SetIdentityTokenProvider(accountTokenProvider{})

			opts := &options.Options{SignerAccount: TestSigningAccount, SummarySignerAccount: tc.account}
			require.NoError(t, di.ensureSummarySigner(opts))
			require.Equal(t, tc.want, signerToken(t, di.summarySigner))

			// The promotion records keep the signer account.
			require.NoError(t, di.ensureAttestationSigner(opts))
			require.Equal(t, "token-for-"+TestSigningAccount, signerToken(t, di.attSigner))

			if tc.shared {
				require.Same(t, di.attSigner, di.summarySigner)
			} else {
				require.NotSame(t, di.attSigner, di.summarySigner)
			}
		})
	}

	t.Run("token provider fails", func(t *testing.T) {
		t.Parallel()

		di := &DefaultPromoterImplementation{}
		di.SetIdentityTokenProvider(&auth.StaticIdentityTokenProvider{Err: errors.New("no credentials")})

		err := di.ensureSummarySigner(&options.Options{SignerAccount: TestSigningAccount, SummarySignerAccount: summaryAccount})
		require.ErrorContains(t, err, summaryAccount)
		require.Nil(t, di.summarySigner)
	})

	t.Run("injected signer is kept", func(t *testing.T) {
		t.Parallel()

		fake := &fakeStatementSigner{}
		di := &DefaultPromoterImplementation{summarySigner: fake}

		require.NoError(t, di.ensureSummarySigner(&options.Options{SummarySignerAccount: summaryAccount}))
		require.Same(t, fake, di.summarySigner)
	})
}

func TestValidateOptionsSummarySignerAccount(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		opts    func(*options.Options)
		wantErr bool
	}{
		{name: "token for the summary signer", opts: func(*options.Options) {}, wantErr: true},
		{name: "dry run", opts: func(o *options.Options) { o.Confirm = false }},
		{name: "summaries off", opts: func(o *options.Options) { o.VerificationSummaries = false }},
		{name: "same account", opts: func(o *options.Options) { o.SummarySignerAccount = o.SignerAccount }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The token is checked before anything is promoted.
			di := &DefaultPromoterImplementation{}
			di.SetIdentityTokenProvider(&auth.StaticIdentityTokenProvider{Err: errors.New("permission denied")})

			opts := &options.Options{
				ThinManifestDir:       "manifests",
				Confirm:               true,
				SignImages:            true,
				MaxSignatureOps:       1,
				VerificationSummaries: true,
				SignerAccount:         TestSigningAccount,
				SummarySignerAccount:  "summaries@example.iam.gserviceaccount.com",
			}
			tc.opts(opts)

			err := di.ValidateOptions(opts)
			if tc.wantErr {
				require.ErrorContains(t, err, "--summary-signer-account")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
