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
	"strings"

	credentials "cloud.google.com/go/iam/credentials/apiv1"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	gopts "google.golang.org/api/option"
	credentialspb "google.golang.org/genproto/googleapis/iam/credentials/v1"

	reg "sigs.k8s.io/promo-tools/v3/internal/legacy/dockerregistry"
	"sigs.k8s.io/promo-tools/v3/internal/legacy/gcloud"
	options "sigs.k8s.io/promo-tools/v3/promoter/image/options"
	"sigs.k8s.io/promo-tools/v3/types/image"
	"sigs.k8s.io/release-sdk/sign"
)

const (
	oidcTokenAudience  = "sigstore"
	signatureTagSuffix = ".sig"

	TestSigningAccount = "k8s-infra-promoter-test-signer@k8s-cip-test-prod.iam.gserviceaccount.com"
	SigningAccount     = "test-signer@ulabs-cloud-tests.iam.gserviceaccount.com"
)

// ValidateStagingSignatures checks if edges (images) have a signature
// applied during its staging run. If they do it verifies them and
// returns an error if they are not valid.
func (di *DefaultPromoterImplementation) ValidateStagingSignatures(
	edges map[reg.PromotionEdge]interface{},
) (map[reg.PromotionEdge]interface{}, error) {
	signer := sign.New(sign.Default())
	signedEdges := map[reg.PromotionEdge]interface{}{}
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
			return nil, errors.Wrapf(err, "checking if %s is signed", imageRef)
		}

		if !isSigned {
			logrus.Infof("No signatures found for ref %s, not checking", imageRef)
			continue
		}

		signedEdges[edge] = edges[edge]

		// Check the staged image signatures
		if _, err := signer.VerifyImage(imageRef); err != nil {
			return nil, errors.Wrapf(
				err, "verifying signatures of image %s", imageRef,
			)
		}
		logrus.Infof("Signatures for ref %s verfified", imageRef)
	}
	return signedEdges, nil
}

// CopySignatures copies sboms and signatures from source images to
// the newly promoted images before stamping them with the
// Kubernetes org signature
func (di *DefaultPromoterImplementation) CopySignatures(
	opts *options.Options, sc *reg.SyncContext, signedEdges map[reg.PromotionEdge]interface{},
) error {
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

	// Options for the new signer
	signOpts := sign.Default()

	// Get the identity token we will use
	token, err := di.GetIdentityToken(opts, SigningAccount)
	if err != nil {
		return errors.Wrap(err, "generating identity token")
	}
	signOpts.IdentityToken = token

	// We only sign the first image of each edge. If there are more
	// than one destination registries for an image, we copy the
	// signature to avoid varying valid signatures in each registry.
	sortedEdges := map[image.Digest][]reg.PromotionEdge{}
	for edge := range edges {
		if _, ok := sortedEdges[edge.Digest]; !ok {
			sortedEdges[edge.Digest] = []reg.PromotionEdge{}
		}
		sortedEdges[edge.Digest] = append(sortedEdges[edge.Digest], edge)
	}

	// Sign the required edges
	for digest := range sortedEdges {
		// Build the reference we will use
		imageRef := fmt.Sprintf(
			"%s/%s@%s",
			sortedEdges[digest][0].DstRegistry.Name,
			sortedEdges[digest][0].DstImageTag.Name,
			sortedEdges[digest][0].Digest,
		)

		// Add all the references as annotations to ensure we
		// get a 2nd signature
		mirrorList := []string{}
		for i := range sortedEdges[digest] {
			mirrorList = append(
				mirrorList, fmt.Sprintf(
					"%s/%s",
					sortedEdges[digest][i].DstRegistry.Name,
					sortedEdges[digest][i].DstImageTag.Name,
				),
			)
		}
		signOpts.Annotations = map[string]interface{}{
			"org.kubernetes.kpromo.mirrors": strings.Join(mirrorList, ","),
		}
		signer := sign.New(signOpts)

		logrus.Infof("Signing image %s", imageRef)
		// Sign the first promoted image in the esges list:
		if _, err := signer.SignImage(imageRef); err != nil {
			return errors.Wrapf(err, "signing image %s", imageRef)
		}

		// If the same digest was promoted to more than one
		// registry, copy the signature from the first one
		if len(sortedEdges[digest]) == 1 {
			logrus.WithField("image", string(digest)).Debug(
				"Not copying signatures, image promoted to single registry",
			)
			continue
		}
		if err := di.copySignatures(
			&sortedEdges[digest][0], sortedEdges[digest][1:],
		); err != nil {
			return fmt.Errorf("copying signatures: %w", err)
		}
	}
	return nil
}

// digestToSignatureTag takes a digest and infers the tag name where
// its signature can be found
func digestToSignatureTag(dg image.Digest) string {
	return strings.ReplaceAll(string(dg), "sha256:", "sha256-") + signatureTagSuffix
}

// copySignatures takes a source edge (an image) and a list of destinations
// and copies the signature to all of them
func (di *DefaultPromoterImplementation) copySignatures(
	src *reg.PromotionEdge, dsts []reg.PromotionEdge,
) error {
	sigTag := digestToSignatureTag(src.Digest)
	sourceRefStr := fmt.Sprintf(
		"%s/%s:%s", src.DstRegistry.Name, src.DstImageTag.Name, sigTag,
	)
	srcRef, err := name.ParseReference(sourceRefStr)
	if err != nil {
		return fmt.Errorf("parsing reference %q: %w", sourceRefStr, err)
	}

	dstRefs := []struct {
		reference name.Reference
		token     gcloud.Token
	}{}

	for i := range dsts {
		ref, err := name.ParseReference(fmt.Sprintf(
			"%s/%s:%s", dsts[i].DstRegistry.Name, dsts[i].DstImageTag.Name, sigTag,
		))
		if err != nil {
			return fmt.Errorf("parsing signature destination referece: %w", err)
		}
		dstRefs = append(dstRefs, struct {
			reference name.Reference
			token     gcloud.Token
		}{ref, dsts[i].DstRegistry.Token})
	}

	// Copy the signatures to the missing registries
	for _, dstRef := range dstRefs {
		if err := crane.Copy(srcRef.String(), dstRef.reference.String()); err != nil {
			return fmt.Errorf(
				"copying signature %s to %s: %w",
				srcRef.String(), dstRef.reference.String(), err,
			)
		}
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
		logrus.Info("Test keyfile set using e2e test credentials")
		// ... and also use the e2e signing identity
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
	logrus.Infof("Signing identity for images will be %s", serviceAccount)
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
