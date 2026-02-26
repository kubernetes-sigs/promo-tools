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
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/gcrane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/sigstore/sigstore/pkg/tuf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/release-sdk/sign"
	"sigs.k8s.io/release-utils/version"

	"sigs.k8s.io/promo-tools/v4/image/consts"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	oidcTokenAudience    = "sigstore"
	signatureTagSuffix   = ".sig"
	sbomTagSuffix        = ".sbom"
	attestationTagSuffix = ".att"

	// intotoMediaType is the media type for in-toto attestation layers.
	intotoMediaType = "application/vnd.dsse.envelope.v1+json"

	TestSigningAccount = "k8s-infra-promoter-test-signer@k8s-cip-test-prod.iam.gserviceaccount.com"
)

// ValidateStagingSignatures checks if edges (images) have a signature
// applied during its staging run. If they do it verifies them and
// returns an error if they are not valid.
func (di *DefaultPromoterImplementation) ValidateStagingSignatures(
	edges map[promotion.Edge]any,
) (map[promotion.Edge]any, error) {
	refsToEdges := map[string]promotion.Edge{}

	for edge := range edges {
		ref := edge.SrcReference()
		refsToEdges[ref] = edge
	}

	refs := make([]string, 0, len(refsToEdges))
	for ref := range refsToEdges {
		refs = append(refs, ref)
	}

	res, err := di.signer.VerifyImages(refs...)
	if err != nil {
		return nil, fmt.Errorf("verify images: %w", err)
	}

	signedEdges := map[promotion.Edge]any{}

	res.Range(func(key, _ any) bool {
		ref, ok := key.(string)
		if !ok {
			logrus.Errorf("Interface conversion failed: key is not a string: %v", key)

			return false
		}

		edge, ok := refsToEdges[ref]
		if !ok {
			logrus.Errorf("Reference %s is not in edge map", ref)

			return true
		}

		signedEdges[edge] = nil

		return true
	})

	return signedEdges, nil
}

// SignImages signs the promoted images and stores their signatures in
// the registry.
func (di *DefaultPromoterImplementation) SignImages(
	opts *options.Options, edges map[promotion.Edge]any,
) error {
	if !opts.SignImages {
		logrus.Info("Not signing images (--sign=false)")

		return nil
	}

	if len(edges) == 0 {
		logrus.Info("No images were promoted. Nothing to sign.")

		return nil
	}

	// Options for the new signer
	signOpts := defaultSignerOptions(opts)

	// Get the identity token we will use
	token, err := di.GetIdentityToken(opts, opts.SignerAccount)
	if err != nil {
		return fmt.Errorf("generating identity token: %w", err)
	}

	signOpts.IdentityToken = token

	// Creating a new Signer after setting the identity token is MANDATORY
	// because that's the only way to propagate the identity token to the
	// internal Signer structs. Without that, the identity token wouldn't be
	// used at all and images would be signed with a wrong identity.
	di.signer = sign.New(signOpts)

	// We only sign the first normalized image per digest of each edge.
	grouped := groupEdgesByIdentityDigest(edges)

	g := new(errgroup.Group)
	g.SetLimit(opts.MaxSignatureOps)

	for _, group := range grouped {
		g.Go(func() error {
			return di.signFirst(signOpts, targetIdentity(&group[0]), &group[0])
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("signing images: %w", err)
	}

	return nil
}

// signFirst signs the first (primary) image for a given identity+digest group.
// Signature replication to additional registries is handled separately by
// ReplicateSignatures.
func (di *DefaultPromoterImplementation) signFirst(signOpts *sign.Options, identity string, edge *promotion.Edge) error {
	imageRef := edge.DstReference()

	// Make a shallow copy so we can safely modify the options per go routine
	signOptsCopy := *signOpts

	// Update the production container identity (".critical.identity.docker-reference")
	signOptsCopy.SignContainerIdentity = identity
	logrus.Infof("Using new production registry reference for %s: %v", imageRef, identity)

	// Add an annotation recording the kpromo version to ensure we
	// get a 2nd signature, otherwise cosign will not resign a signed image:
	signOptsCopy.Annotations = []string{
		"org.kubernetes.kpromo.version=kpromo-" + version.GetVersionInfo().GitVersion,
	}

	logrus.Infof("Signing image %s", imageRef)

	// Carry over existing signatures from the staging repo
	if err := di.copyAttachedObjects(edge); err != nil {
		return fmt.Errorf("copying staging signatures: %w", err)
	}

	// Sign the promoted image:
	if _, err := di.signer.SignImageWithOptions(&signOptsCopy, imageRef); err != nil {
		return fmt.Errorf("signing image %s: %w", imageRef, err)
	}

	return nil
}

// ReplicateSignatures copies signatures from the primary destination registry
// to all additional destination registries for images that were promoted to
// multiple registries.
func (di *DefaultPromoterImplementation) ReplicateSignatures(
	opts *options.Options, edges map[promotion.Edge]any,
) error {
	if !opts.SignImages {
		logrus.Info("Signing disabled, skipping signature replication")

		return nil
	}

	if len(edges) == 0 {
		logrus.Info("No images were promoted. Nothing to replicate.")

		return nil
	}

	grouped := groupEdgesByIdentityDigest(edges)

	g := new(errgroup.Group)
	g.SetLimit(opts.MaxSignatureCopies)

	for _, group := range grouped {
		if len(group) <= 1 {
			continue
		}

		g.Go(func() error {
			return di.replicateSignatures(&group[0], group[1:])
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("replicating signatures: %w", err)
	}

	return nil
}

// targetIdentity returns the production identity for a promotion edge.
//
// This means we will substitute the .critical.identity.docker-reference within
// an example signature:
// 'us-west2-docker.pkg.dev/k8s-artifacts-prod/images/kubernetes/conformance-arm64'
//
// to match the production registry:
// 'registry.k8s.io/kubernetes/conformance-arm64'.
func targetIdentity(edge *promotion.Edge) string {
	identity := fmt.Sprintf("%s/%s", edge.DstRegistry.Name, edge.DstImageTag.Name)

	if !strings.Contains(string(edge.DstRegistry.Name), productionRepositoryPath) {
		logrus.Infof(
			"No production registry path %q used in image, not modifying target signature reference",
			productionRepositoryPath,
		)

		return identity
	}

	idx := strings.Index(identity, productionRepositoryPath) + len(productionRepositoryPath)
	newRef := consts.ProdRegistry + identity[idx:]

	return newRef
}

// groupEdgesByIdentityDigest groups promotion edges by their target identity
// and digest. Within each group, edges are sorted by destination registry name
// to ensure deterministic ordering across calls. The first edge in each group
// is used as the primary for signing and as the source for replication.
func groupEdgesByIdentityDigest(edges map[promotion.Edge]any) [][]promotion.Edge {
	type key struct {
		identity string
		digest   image.Digest
	}

	grouped := map[key][]promotion.Edge{}

	for edge := range edges {
		// Skip metadata layers
		if strings.HasSuffix(string(edge.DstImageTag.Tag), ".sig") ||
			strings.HasSuffix(string(edge.DstImageTag.Tag), ".att") ||
			edge.DstImageTag.Tag == "" {
			continue
		}

		k := key{identity: targetIdentity(&edge), digest: edge.Digest}
		grouped[k] = append(grouped[k], edge)
	}

	// Sort edges within each group by destination registry name so that
	// SignImages and ReplicateSignatures agree on which edge is primary.
	result := make([][]promotion.Edge, 0, len(grouped))
	for _, group := range grouped {
		sort.Slice(group, func(i, j int) bool {
			return string(group[i].DstRegistry.Name) < string(group[j].DstRegistry.Name)
		})
		result = append(result, group)
	}

	return result
}

// copyAttachedObjects copies any attached signatures from the staging registry to
// the production registry. The function is called copyAttachedObjects as it will
// move attestations and SBOMs too once we stabilize the signing code.
func (di *DefaultPromoterImplementation) copyAttachedObjects(edge *promotion.Edge) error {
	sigTag := digestToSignatureTag(edge.Digest)
	srcRefString := fmt.Sprintf(
		"%s/%s:%s", edge.SrcRegistry.Name, edge.SrcImageTag.Name, sigTag,
	)

	srcRef, err := name.ParseReference(srcRefString)
	if err != nil {
		return fmt.Errorf("parsing signed source reference %s: %w", srcRefString, err)
	}

	dstRefString := fmt.Sprintf(
		"%s/%s:%s", edge.DstRegistry.Name, edge.DstImageTag.Name, sigTag,
	)

	dstRef, err := name.ParseReference(dstRefString)
	if err != nil {
		return fmt.Errorf("parsing reference: %w", err)
	}

	logrus.Infof("Signature pre copy: %s to %s", srcRefString, dstRefString)

	craneOpts := []crane.Option{
		crane.WithAuthFromKeychain(gcrane.Keychain),
		crane.WithUserAgent(image.UserAgent),
		crane.WithTransport(di.getSigningTransport()),
	}

	if err := crane.Copy(srcRef.String(), dstRef.String(), craneOpts...); err != nil {
		// If the signature layer does not exist it means that the src image
		// is not signed, so we catch the error and return nil
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			logrus.Debugf("Reference %s is not signed, not copying", srcRef.String())

			return nil
		}

		return fmt.Errorf(
			"copying signature %s to %s: %w", srcRef.String(), dstRef.String(), err,
		)
	}

	return nil
}

// digestToSignatureTag takes a digest and infers the tag name where
// its signature can be found.
func digestToSignatureTag(dg image.Digest) string {
	return strings.ReplaceAll(string(dg), "sha256:", "sha256-") + signatureTagSuffix
}

// replicateSignatures takes a source edge (an image) and a list of destinations
// and copies the signature to all of them.
func (di *DefaultPromoterImplementation) replicateSignatures(
	src *promotion.Edge, dsts []promotion.Edge,
) error {
	sigTag := digestToSignatureTag(src.Digest)
	sourceRefStr := fmt.Sprintf(
		"%s/%s:%s", src.DstRegistry.Name, src.DstImageTag.Name, sigTag,
	)

	srcRef, err := name.ParseReference(sourceRefStr)
	if err != nil {
		return fmt.Errorf("parsing reference %q: %w", sourceRefStr, err)
	}

	// Check if the source signature exists before iterating mirrors.
	// This avoids 20+ unnecessary HEAD requests per unsigned image.
	if err := di.headWithRetry(srcRef); err != nil {
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			logrus.WithField("src", sourceRefStr).Debug("Source signature not found, skipping group")

			return nil
		}

		return fmt.Errorf("checking source signature %s: %w", sourceRefStr, err)
	}

	logrus.WithField("src", sourceRefStr).Infof("Replicating signature to %d images", len(dsts))

	dstRefs := []name.Reference{}

	for i := range dsts {
		ref, err := name.ParseReference(fmt.Sprintf(
			"%s/%s:%s", dsts[i].DstRegistry.Name, dsts[i].DstImageTag.Name, sigTag,
		))
		if err != nil {
			return fmt.Errorf("parsing signature destination reference: %w", err)
		}

		dstRefs = append(dstRefs, ref)
	}

	// Copy the signatures to the missing registries in parallel.
	g := new(errgroup.Group)
	for _, dstRef := range dstRefs {
		g.Go(func() error {
			// Skip if the signature tag already exists at the destination.
			if _, err := remote.Head(dstRef,
				remote.WithAuthFromKeychain(gcrane.Keychain),
				remote.WithTransport(di.getSigningTransport()),
			); err == nil {
				logrus.WithField("dst", dstRef.String()).Debug("Signature already exists, skipping")

				return nil
			}

			logrus.WithField("src", srcRef.String()).Infof("replication > %s", dstRef.String())

			opts := []crane.Option{
				crane.WithAuthFromKeychain(gcrane.Keychain),
				crane.WithUserAgent(image.UserAgent),
				crane.WithTransport(di.getSigningTransport()),
			}
			if err := di.copyWithRetry(srcRef.String(), dstRef.String(), opts); err != nil {
				var terr *transport.Error
				if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
					logrus.Debugf("Signature %s not found, skipping", srcRef.String())

					return nil
				}

				return fmt.Errorf(
					"copying signature %s to %s: %w",
					srcRef.String(), dstRef.String(), err,
				)
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("replicating signatures: %w", err)
	}

	return nil
}

// retryBackoff defines the exponential backoff for transient registry errors.
var retryBackoff = wait.Backoff{
	Duration: 30 * time.Second,
	Factor:   2,
	Jitter:   0.1,
	Steps:    3,
}

// isTransient returns true for HTTP status codes that indicate a temporary
// failure worth retrying (429 Too Many Requests and 5xx server errors).
func isTransient(err error) bool {
	var terr *transport.Error
	if errors.As(err, &terr) {
		return terr.StatusCode == http.StatusTooManyRequests ||
			terr.StatusCode >= http.StatusInternalServerError
	}

	return false
}

// withRetry calls fn with exponential backoff on transient registry errors.
// Non-transient errors are returned immediately.
func withRetry(fn func() error) error {
	var lastErr error

	err := wait.ExponentialBackoff(retryBackoff, func() (bool, error) {
		lastErr = fn()
		if lastErr == nil {
			return true, nil // success, stop retrying
		}

		if !isTransient(lastErr) {
			return false, lastErr // permanent error, stop retrying
		}

		return false, nil // transient error, keep retrying
	})
	if wait.Interrupted(err) {
		return lastErr // retries exhausted, return the last transient error
	}

	if err != nil {
		return fmt.Errorf("exponential backoff: %w", err)
	}

	return nil
}

// headWithRetry performs a remote.Head with retries on transient errors.
func (di *DefaultPromoterImplementation) headWithRetry(ref name.Reference) error {
	return withRetry(func() error {
		_, err := remote.Head(ref,
			remote.WithAuthFromKeychain(gcrane.Keychain),
			remote.WithTransport(di.getSigningTransport()),
		)
		if err != nil {
			return fmt.Errorf("remote head %s: %w", ref.String(), err)
		}

		return nil
	})
}

// copyWithRetry performs a crane.Copy with retries on transient errors.
func (di *DefaultPromoterImplementation) copyWithRetry(src, dst string, opts []crane.Option) error {
	return withRetry(func() error {
		return crane.Copy(src, dst, opts...)
	})
}

// WriteSBOMs copies pre-generated SBOMs from the staging registry to each
// production registry for the promoted images. SBOMs are expected to be
// attached in staging (e.g., by the build system) and are identified by
// the cosign SBOM tag convention (sha256-<hash>.sbom).
func (di *DefaultPromoterImplementation) WriteSBOMs(
	opts *options.Options, edges map[promotion.Edge]any,
) error {
	if len(edges) == 0 {
		logrus.Info("No images were promoted. No SBOMs to copy.")

		return nil
	}

	g := new(errgroup.Group)
	g.SetLimit(opts.MaxSignatureCopies)

	for edge := range edges {
		// Skip signature and attestation layers
		if strings.HasSuffix(string(edge.DstImageTag.Tag), ".sig") ||
			strings.HasSuffix(string(edge.DstImageTag.Tag), ".att") ||
			strings.HasSuffix(string(edge.DstImageTag.Tag), ".sbom") ||
			edge.DstImageTag.Tag == "" {
			continue
		}

		g.Go(func() error {
			if err := di.copySBOM(&edge); err != nil {
				return fmt.Errorf("copying SBOM for %s: %w", edge.DstReference(), err)
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("writing SBOMs: %w", err)
	}

	return nil
}

// copySBOM copies an SBOM from the staging registry to the production registry
// for a single promotion edge. If no SBOM exists in staging, this is not an error.
func (di *DefaultPromoterImplementation) copySBOM(edge *promotion.Edge) error {
	sbomTag := digestToSBOMTag(edge.Digest)
	srcRefString := fmt.Sprintf(
		"%s/%s:%s", edge.SrcRegistry.Name, edge.SrcImageTag.Name, sbomTag,
	)
	dstRefString := fmt.Sprintf(
		"%s/%s:%s", edge.DstRegistry.Name, edge.DstImageTag.Name, sbomTag,
	)

	craneOpts := []crane.Option{
		crane.WithAuthFromKeychain(gcrane.Keychain),
		crane.WithUserAgent(image.UserAgent),
		crane.WithTransport(di.getSigningTransport()),
	}

	logrus.Infof("SBOM copy: %s to %s", srcRefString, dstRefString)

	if err := crane.Copy(srcRefString, dstRefString, craneOpts...); err != nil {
		// If the SBOM does not exist in staging, skip silently
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			logrus.Debugf("No SBOM found for %s, skipping", srcRefString)

			return nil
		}

		return fmt.Errorf("copying SBOM %s to %s: %w", srcRefString, dstRefString, err)
	}

	return nil
}

// digestToSBOMTag takes a digest and infers the tag name where
// its SBOM can be found.
func digestToSBOMTag(dg image.Digest) string {
	return strings.ReplaceAll(string(dg), "sha256:", "sha256-") + sbomTagSuffix
}

// GetIdentityToken returns an identity token for the selected service account
// in order for this function to work, an account has to be already logged.
func (di *DefaultPromoterImplementation) GetIdentityToken(
	_ *options.Options, serviceAccount string,
) (string, error) {
	// If the test signer file is found switch to test credentials
	if os.Getenv("CIP_E2E_KEY_FILE") != "" {
		logrus.Info("Test keyfile set using e2e test credentials")

		serviceAccount = TestSigningAccount
	}

	token, err := di.identityTokenProvider.GetIdentityToken(
		context.Background(), serviceAccount, oidcTokenAudience,
	)
	if err != nil {
		return "", fmt.Errorf("getting identity token: %w", err)
	}

	return token, nil
}

// WriteProvenanceAttestations generates SLSA provenance attestations for
// promoted images and pushes them as .att tags to the destination registry.
func (di *DefaultPromoterImplementation) WriteProvenanceAttestations(
	_ *options.Options,
	edges map[promotion.Edge]any,
	generator provenance.Generator,
) error {
	if len(edges) == 0 {
		logrus.Info("No images were promoted. No provenance to generate.")

		return nil
	}

	builderID := "https://k8s.io/promo-tools"
	if v := version.GetVersionInfo().GitVersion; v != "" {
		builderID += "@" + v
	}

	ctx := context.Background()
	now := time.Now()

	for edge := range edges {
		// Skip metadata layers
		tag := string(edge.DstImageTag.Tag)
		if strings.HasSuffix(tag, ".sig") ||
			strings.HasSuffix(tag, ".att") ||
			strings.HasSuffix(tag, ".sbom") ||
			tag == "" {
			continue
		}

		record := provenance.PromotionRecord{
			SrcRef:    edge.SrcReference(),
			DstRef:    edge.DstReference(),
			Digest:    string(edge.Digest),
			Timestamp: now,
			BuilderID: builderID,
		}

		if err := di.pushAttestation(ctx, &edge, generator, &record); err != nil {
			return fmt.Errorf("writing provenance for %s: %w", edge.DstReference(), err)
		}
	}

	return nil
}

// pushAttestation generates and pushes a provenance attestation as an
// OCI image with an .att tag.
func (di *DefaultPromoterImplementation) pushAttestation(
	ctx context.Context,
	edge *promotion.Edge,
	generator provenance.Generator,
	record *provenance.PromotionRecord,
) error {
	attestation, err := generator.Generate(ctx, record)
	if err != nil {
		return fmt.Errorf("generating attestation: %w", err)
	}

	attTag := digestToAttestationTag(edge.Digest)
	dstRefString := fmt.Sprintf(
		"%s/%s:%s", edge.DstRegistry.Name, edge.DstImageTag.Name, attTag,
	)

	ref, err := name.ParseReference(dstRefString)
	if err != nil {
		return fmt.Errorf("parsing attestation reference %s: %w", dstRefString, err)
	}

	// Check if attestation already exists (idempotent)
	if _, err := remote.Head(ref, remote.WithAuthFromKeychain(gcrane.Keychain)); err == nil {
		logrus.Debugf("Attestation %s already exists, skipping", dstRefString)

		return nil
	}

	// Create an OCI image with the attestation as a single layer
	layer := static.NewLayer(attestation, types.MediaType(intotoMediaType))

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		return fmt.Errorf("creating attestation image: %w", err)
	}

	// Set the config media type to mark this as an attestation
	img = mutate.MediaType(img, types.OCIManifestSchema1)
	img = mutate.ConfigMediaType(img, types.MediaType("application/vnd.oci.image.config.v1+json"))

	logrus.Infof("Provenance attestation: pushing %s", dstRefString)

	if err := remote.Write(ref, img,
		remote.WithAuthFromKeychain(gcrane.Keychain),
		remote.WithUserAgent(image.UserAgent),
		remote.WithTransport(di.getSigningTransport()),
	); err != nil {
		return fmt.Errorf("pushing attestation %s: %w", dstRefString, err)
	}

	return nil
}

// digestToAttestationTag takes a digest and infers the tag name where
// its attestation can be found.
func digestToAttestationTag(dg image.Digest) string {
	return strings.ReplaceAll(string(dg), "sha256:", "sha256-") + attestationTagSuffix
}

// PrewarmTUFCache initializes the TUF cache so that threads do not have to compete
// against each other creating the TUF database.
func (di *DefaultPromoterImplementation) PrewarmTUFCache() error {
	if err := tuf.Initialize(
		context.Background(), tuf.DefaultRemoteRoot, nil,
	); err != nil {
		return fmt.Errorf("initializing TUF client: %w", err)
	}

	return nil
}
