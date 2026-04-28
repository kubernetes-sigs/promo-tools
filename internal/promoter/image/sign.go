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
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/types"
	ocimutate "github.com/sigstore/cosign/v2/pkg/oci/mutate"
	ociremote "github.com/sigstore/cosign/v2/pkg/oci/remote"
	"github.com/sigstore/cosign/v2/pkg/oci/static"
	ctypes "github.com/sigstore/cosign/v2/pkg/types"
	"github.com/sigstore/sigstore/pkg/tuf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/types/known/timestamppb"
	"sigs.k8s.io/release-sdk/sign"
	"sigs.k8s.io/release-utils/version"

	"sigs.k8s.io/promo-tools/v4/image/consts"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	oidcTokenAudience  = "sigstore"
	signatureTagSuffix = ".sig"

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
	di.signOpts = signOpts

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

// targetIdentity returns the production identity for a promotion edge.
//
// This means we will substitute the .critical.identity.docker-reference within
// an example signature:
// 'us-west2-docker.pkg.dev/k8s-artifacts-prod/images/kubernetes/conformance-arm64'
//
// to match the production registry:
// 'registry.k8s.io/kubernetes/conformance-arm64'.
func targetIdentity(edge *promotion.Edge) string {
	identity := string(edge.DstRegistry.Name) + "/" + string(edge.DstImageTag.Name)

	if !strings.Contains(string(edge.DstRegistry.Name), productionRepositoryPath) {
		logrus.Debugf(
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
// and digest. Within each group, edges are sorted so that the canonical
// registry (canonicalRegistry) comes first, with remaining registries in
// alphabetical order. The first edge in each group is used as the signing
// target.
func groupEdgesByIdentityDigest(edges map[promotion.Edge]any) [][]promotion.Edge {
	type key struct {
		identity string
		digest   image.Digest
	}

	grouped := make(map[key][]promotion.Edge, len(edges)/2)

	for edge := range edges {
		delete(edges, edge) // free as we go to reduce peak memory

		// Skip metadata layers
		if strings.HasSuffix(string(edge.DstImageTag.Tag), ".sig") ||
			strings.HasSuffix(string(edge.DstImageTag.Tag), ".att") ||
			edge.DstImageTag.Tag == "" {
			continue
		}

		k := key{identity: targetIdentity(&edge), digest: edge.Digest}
		grouped[k] = append(grouped[k], edge)
	}

	result := make([][]promotion.Edge, 0, len(grouped))
	for _, group := range grouped {
		sort.Slice(group, func(i, j int) bool {
			iCanonical := strings.HasPrefix(string(group[i].DstRegistry.Name), canonicalRegistry)

			jCanonical := strings.HasPrefix(string(group[j].DstRegistry.Name), canonicalRegistry)
			if iCanonical != jCanonical {
				return iCanonical
			}

			return string(group[i].DstRegistry.Name) < string(group[j].DstRegistry.Name)
		})
		result = append(result, group)
	}

	return result
}

// copyAttachedObjects copies any attached signatures from the staging registry to
// the production registry.
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

	if err := ratelimit.WithRetry(func() error {
		return craneCopyWithTimeout(context.TODO(), srcRef.String(), dstRef.String(), ratelimit.CopyTimeout, di.craneOptions())
	}); err != nil {
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

// GetIdentityToken returns an identity token for the selected service account
// in order for this function to work, an account has to be already logged.
func (di *DefaultPromoterImplementation) GetIdentityToken(
	_ *options.Options, serviceAccount string,
) (string, error) {
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
	ctx context.Context,
	opts *options.Options,
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

	now := time.Now()

	g := new(errgroup.Group)
	g.SetLimit(opts.MaxSignatureOps)

	for edge := range edges {
		// Skip metadata layers
		tag := string(edge.DstImageTag.Tag)
		if strings.HasSuffix(tag, ".sig") ||
			strings.HasSuffix(tag, ".att") ||
			tag == "" {
			continue
		}

		record := provenance.PromotionRecord{
			SrcRef:    edge.SrcReference(),
			DstRef:    edge.DstReference(),
			Digest:    string(edge.Digest),
			Timestamp: timestamppb.New(now),
			BuilderId: builderID,
		}

		g.Go(func() error {
			if err := di.pushAttestation(ctx, &edge, generator, &record); err != nil {
				return fmt.Errorf("writing provenance for %s: %w", edge.DstReference(), err)
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("writing provenance attestations: %w", err)
	}

	return nil
}

// pushAttestation generates and pushes a provenance attestation as a
// layer in the .att image for the destination digest. The layer includes
// a predicateType annotation for idempotency checking and compatibility
// with cosign's attestation conventions.
func (di *DefaultPromoterImplementation) pushAttestation(
	ctx context.Context,
	edge *promotion.Edge,
	generator provenance.Generator,
	record *provenance.PromotionRecord,
) error {
	payload, err := generator.Generate(ctx, record)
	if err != nil {
		return fmt.Errorf("generating attestation: %w", err)
	}

	// Build the digest reference for the destination image.
	dstDigestRef := fmt.Sprintf(
		"%s/%s@%s", edge.DstRegistry.Name, edge.DstImageTag.Name, edge.Digest,
	)

	digest, err := name.NewDigest(dstDigestRef)
	if err != nil {
		return fmt.Errorf("parsing digest reference %s: %w", dstDigestRef, err)
	}

	remoteOpt := ociremote.WithRemoteOptions(di.remoteOptions()...)

	// Check if our predicate type already exists (idempotent).
	if hasPredicateType(digest, provenance.PredicateType, remoteOpt) {
		logrus.Debugf("Attestation for %s already exists, skipping", dstDigestRef)

		return nil
	}

	// Create the attestation layer with predicate type annotation.
	att, err := static.NewAttestation(payload,
		static.WithLayerMediaType(types.MediaType(ctypes.IntotoPayloadType)),
		static.WithAnnotations(map[string]string{
			"predicateType": provenance.PredicateType,
		}),
	)
	if err != nil {
		return fmt.Errorf("creating attestation: %w", err)
	}

	// Get the existing signed entity for this digest and append.
	se := ociremote.SignedUnknown(digest, remoteOpt)

	newSE, err := ocimutate.AttachAttestationToEntity(se, att)
	if err != nil {
		return fmt.Errorf("attaching attestation: %w", err)
	}

	logrus.Infof("Provenance attestation: pushing for %s", dstDigestRef)

	if err := ratelimit.WithRetry(func() error {
		return ociremote.WriteAttestations(digest.Context(), newSE, remoteOpt)
	}); err != nil {
		return fmt.Errorf("pushing attestation for %s: %w", dstDigestRef, err)
	}

	return nil
}

// hasPredicateType checks if the .att image for the given digest already
// contains a layer with the specified predicateType annotation.
func hasPredicateType(digest name.Digest, predicateType string, opts ...ociremote.Option) bool {
	se := ociremote.SignedUnknown(digest, opts...)

	atts, err := se.Attestations()
	if err != nil {
		return false
	}

	sigs, err := atts.Get()
	if err != nil {
		return false
	}

	for _, sig := range sigs {
		ann, err := sig.Annotations()
		if err != nil {
			continue
		}

		if ann["predicateType"] == predicateType {
			return true
		}
	}

	return false
}

// craneCopyWithTimeout wraps crane.Copy with a per-request context timeout.
// It copies the opts slice to avoid mutating the caller's backing array.
func craneCopyWithTimeout(ctx context.Context, src, dst string, timeout time.Duration, opts []crane.Option) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	withCtx := make([]crane.Option, len(opts), len(opts)+1)
	copy(withCtx, opts)
	withCtx = append(withCtx, crane.WithContext(ctx))

	//nolint:wrapcheck // callers add their own context-specific wrapping
	return crane.Copy(src, dst, withCtx...)
}

// PrewarmTUFCache initializes the TUF cache so that threads do not have to compete
// against each other creating the TUF database.
func (di *DefaultPromoterImplementation) PrewarmTUFCache(ctx context.Context) error {
	if err := tuf.Initialize(
		ctx, tuf.DefaultRemoteRoot, nil,
	); err != nil {
		return fmt.Errorf("initializing TUF client: %w", err)
	}

	return nil
}
