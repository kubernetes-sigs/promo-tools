/*
Copyright 2022 The Kubernetes Authors.

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
	"fmt"
	"os"

	credentials "cloud.google.com/go/iam/credentials/apiv1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	gopts "google.golang.org/api/option"
	credentialspb "google.golang.org/genproto/googleapis/iam/credentials/v1"

	reg "sigs.k8s.io/promo-tools/v3/internal/legacy/dockerregistry"
	options "sigs.k8s.io/promo-tools/v3/promoter/image/options"
	"sigs.k8s.io/release-sdk/sign"
)

const (
	oidcTokenAudience = "sigstore"

	TestSigningAccount = "k8s-infra-promoter-test-signer@k8s-cip-test-prod.iam.gserviceaccount.com"
	SigningAccount     = "test-signer@ulabs-cloud-tests.iam.gserviceaccount.com"
)

// ValidateStagingSignatures checks if edges (images) have a signature
// applied during its staging run. If they do it verifies them and
// returns an error if they are not valid.
func (di *DefaultPromoterImplementation) ValidateStagingSignatures(
	edges map[reg.PromotionEdge]interface{},
) error {
	signer := sign.New(sign.Default())

	for edge := range edges {
		imageRef := fmt.Sprintf(
			"%s/%s@%s",
			edge.SrcRegistry.Name,
			edge.SrcImageTag.Name,
			edge.Digest,
		)
		logrus.Infof("Verifying signatures of image %s", imageRef)

		// If image is not signed, skip
		isSigned, err := signer.IsImageSigned(imageRef)
		if err != nil {
			return errors.Wrapf(err, "checking if %s is signed", imageRef)
		}

		if !isSigned {
			logrus.Infof("No signatures found for ref %s, not checking", imageRef)
			continue
		}

		// Check the staged image signatures
		if _, err := signer.VerifyImage(imageRef); err != nil {
			return errors.Wrapf(
				err, "verifying signatures of image %s", imageRef,
			)
		}
		logrus.Infof("Signatures for ref %s verfified", imageRef)
	}
	return nil
}

// SignImages signs the promoted images and stores their signatures in
// the registry
func (di *DefaultPromoterImplementation) SignImages(
	opts *options.Options, sc *reg.SyncContext, edges map[reg.PromotionEdge]interface{},
) error {
	if len(edges) == 0 {
		logrus.Info("No images were promoted. Nothing to sign.")
		return nil
	}
	token, err := di.GetIdentityToken(opts, TestSigningAccount)
	if err != nil {
		return errors.Wrap(err, "generating identity token")
	}
	signOpts := sign.Default()
	signOpts.IdentityToken = token
	signer := sign.New(signOpts)

	for edge := range edges {
		imageRef := fmt.Sprintf(
			"%s/%s@%s",
			edge.DstRegistry.Name,
			edge.DstImageTag.Name,
			edge.Digest,
		)
		if _, err := signer.SignImage(imageRef); err != nil {
			return errors.Wrapf(err, "signing image %s", imageRef)
		}
		logrus.Infof("Signing image %s", imageRef)
	}
	return nil
}

// WriteSBOMs writes SBOMs to each of the newly promoted images and stores
// them along the signatures in the registry
func (di *DefaultPromoterImplementation) WriteSBOMs(
	opts *options.Options, sc *reg.SyncContext, edges map[reg.PromotionEdge]interface{},
) error {
	return nil
}

// GetIdentityToken returns an identity token for the selected service account
// in order for this function to work, an account has to be already logged. This
// can be achieved using the
func (di *DefaultPromoterImplementation) GetIdentityToken(
	opts *options.Options, serviceAccount string,
) (tok string, err error) {
	credOptions := []gopts.ClientOption{}
	// If the test signer file is found switch to test credentials
	if os.Getenv("CIP_E2E_KEY_FILE") != "" {
		logrus.Infof("Test keyfile set using e2e test credentials")
		serviceAccount = TestSigningAccount
		credOptions = []gopts.ClientOption{
			gopts.WithCredentialsFile(os.Getenv("CIP_E2E_KEY_FILE")),
		}
	}

	// If SignerInitCredentials, initialize the iam client using
	// the identityu in that file instead of Default Application Credentials
	if opts.SignerInitCredentials != "" {
		logrus.Infof("Using credentials from %s", opts.SignerInitCredentials)
		credOptions = []gopts.ClientOption{
			gopts.WithCredentialsFile(opts.SignerInitCredentials),
		}
	}
	ctx := context.Background()
	c, err := credentials.NewIamCredentialsClient(
		ctx, credOptions...,
	)
	if err != nil {
		return tok, errors.Wrap(err, "creating credentials token")
	}
	defer c.Close()
	req := &credentialspb.GenerateIdTokenRequest{
		Name:         fmt.Sprintf("projects/-/serviceAccounts/%s", serviceAccount),
		Audience:     oidcTokenAudience, // Should be set to "sigstore"
		IncludeEmail: true,
	}

	resp, err := c.GenerateIdToken(ctx, req)
	if err != nil {
		return tok, errors.Wrap(err, "getting error account")
	}

	logrus.Info(resp.Token)

	return resp.Token, nil
}
