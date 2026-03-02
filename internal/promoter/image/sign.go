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
	"sync"
	"sync/atomic"
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

// ReplicateSignatures batch-lists tags for all image repositories across all
// registries in a single concurrent pass, then copies only the signatures
// that are missing from the mirrors. This is used by the standalone
// replication pipeline where most signatures already exist.
func (di *DefaultPromoterImplementation) ReplicateSignatures(
	opts *options.Options, edges map[promotion.Edge]any,
) error {
	if !opts.SignImages {
		logrus.Info("Signing disabled, skipping signature replication")

		return nil
	}

	if len(edges) == 0 {
		logrus.Info("No edges. Nothing to replicate.")

		return nil
	}

	multiGroups := collectMultiRegistryGroups(edges)
	if len(multiGroups) == 0 {
		logrus.Info("No multi-registry groups to replicate")

		return nil
	}

	copies, err := di.computeCopiesFromInventory(multiGroups)
	if err != nil {
		return fmt.Errorf("computing copies from inventory: %w", err)
	}

	if len(copies) == 0 {
		logrus.Info("All signatures already replicated")

		return nil
	}

	return di.executeCopies(opts, copies)
}

// collectMultiRegistryGroups groups edges by identity+digest, keeps only
// groups with more than one registry, and sorts them deterministically.
func collectMultiRegistryGroups(edges map[promotion.Edge]any) [][]promotion.Edge {
	grouped := groupEdgesByIdentityDigest(edges)

	multiGroups := make([][]promotion.Edge, 0, len(grouped))

	for _, group := range grouped {
		if len(group) > 1 {
			multiGroups = append(multiGroups, group)
		}
	}

	sort.Slice(multiGroups, func(i, j int) bool {
		return multiGroups[i][0].DstReference() < multiGroups[j][0].DstReference()
	})

	return multiGroups
}

// executeCopies runs the given signature copies concurrently with bounded
// parallelism and progress logging.
func (di *DefaultPromoterImplementation) executeCopies(
	opts *options.Options, copies []copyItem,
) error {
	logrus.Infof("Copying %d signatures", len(copies))

	var completed atomic.Int64

	total := int64(len(copies))

	g := new(errgroup.Group)
	g.SetLimit(opts.MaxSignatureCopies)

	for _, c := range copies {
		g.Go(func() error {
			craneOpts := []crane.Option{
				crane.WithAuthFromKeychain(gcrane.Keychain),
				crane.WithUserAgent(image.UserAgent),
				crane.WithTransport(di.getTransport()),
			}

			if err := di.copyWithRetry(c.src, c.dst, craneOpts); err != nil {
				var terr *transport.Error
				if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
					logrus.Debugf("Signature %s not found, skipping (%d/%d)",
						c.src, completed.Add(1), total)

					return nil
				}

				return fmt.Errorf("copying signature %s to %s: %w",
					c.src, c.dst, err)
			}

			logrus.Infof("Copied signature %s (%d/%d)",
				c.dst, completed.Add(1), total)

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("copying signatures: %w", err)
	}

	return nil
}

type copyItem struct{ src, dst string }

// computeCopiesFromInventory batch-lists tags for all repositories across
// source and mirrors in a single concurrent pass, then returns only the
// copies where the source has a signature tag that the mirror is missing.
func (di *DefaultPromoterImplementation) computeCopiesFromInventory(
	multiGroups [][]promotion.Edge,
) ([]copyItem, error) {
	type repoKey struct{ registry, image string }

	type tagSet = map[string]struct{}

	allRepos := map[repoKey]struct{}{}

	for _, group := range multiGroups {
		for _, edge := range group {
			key := repoKey{string(edge.DstRegistry.Name), string(edge.DstImageTag.Name)}
			allRepos[key] = struct{}{}
		}
	}

	totalRepos := len(allRepos)

	logrus.Infof("Listing tags for %d repositories across %d groups",
		totalRepos, len(multiGroups))

	// Temporarily increase the rate limit during read-only listing.
	// The AR quota is ~83 req/sec; we use 80 for headroom. The normal
	// limit (50) is restored after the batch completes.
	rt := di.getTransport()
	rt.SetLimit(ratelimit.ListingLimit)
	rt.SetBurst(ratelimit.ListingBurst)

	defer func() {
		rt.SetLimit(ratelimit.MaxEvents)
		rt.SetBurst(ratelimit.DefaultBurst)
	}()

	allTags := make(map[repoKey]tagSet, totalRepos)

	var (
		mu     sync.Mutex
		listed atomic.Int64
	)

	g := new(errgroup.Group)
	g.SetLimit(ratelimit.ListingConcurrency)

	for key := range allRepos {
		g.Go(func() error {
			tags, err := di.listTagsWithRetry(
				fmt.Sprintf("%s/%s", key.registry, key.image),
			)
			if err != nil {
				return err
			}

			set := make(tagSet, len(tags))
			for _, t := range tags {
				set[t] = struct{}{}
			}

			mu.Lock()
			allTags[key] = set
			mu.Unlock()

			if n := listed.Add(1); n%1000 == 0 {
				logrus.Infof("Listed %d/%d repositories", n, totalRepos)
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("listing repositories: %w", err)
	}

	logrus.Infof("Listed %d repositories", totalRepos)

	var (
		copies      []copyItem
		seen        = map[string]struct{}{}
		signedCount int
	)

	for _, group := range multiGroups {
		src := group[0]
		srcKey := repoKey{string(src.DstRegistry.Name), string(src.DstImageTag.Name)}
		sigTag := digestToSignatureTag(src.Digest)

		if _, ok := allTags[srcKey][sigTag]; !ok {
			continue
		}

		signedCount++

		srcRef := fmt.Sprintf("%s/%s:%s",
			src.DstRegistry.Name, src.DstImageTag.Name, sigTag)

		for _, dst := range group[1:] {
			dstRef := fmt.Sprintf("%s/%s:%s",
				dst.DstRegistry.Name, dst.DstImageTag.Name, sigTag)

			if _, ok := seen[dstRef]; ok {
				continue
			}

			seen[dstRef] = struct{}{}

			dstKey := repoKey{string(dst.DstRegistry.Name), string(dst.DstImageTag.Name)}

			if _, ok := allTags[dstKey][sigTag]; !ok {
				copies = append(copies, copyItem{srcRef, dstRef})
			}
		}
	}

	logrus.Infof("Signature status: %d/%d groups signed, %d copies needed",
		signedCount, len(multiGroups), len(copies))

	return copies, nil
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
		crane.WithTransport(di.getTransport()),
	}

	if err := ratelimit.WithRetry(func() error {
		return crane.Copy(srcRef.String(), dstRef.String(), craneOpts...)
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

// copyWithRetry performs a crane.Copy with retries on transient errors.
func (di *DefaultPromoterImplementation) copyWithRetry(src, dst string, opts []crane.Option) error {
	if err := ratelimit.WithRetry(func() error {
		return crane.Copy(src, dst, opts...)
	}); err != nil {
		return fmt.Errorf("copying %s to %s: %w", src, dst, err)
	}

	return nil
}

// listTagsWithRetry lists all tags for a repository with retries on transient
// errors. Returns nil (not an error) if the repository does not exist.
func (di *DefaultPromoterImplementation) listTagsWithRetry(repo string) ([]string, error) {
	var tags []string

	if err := ratelimit.WithRetry(func() error {
		var err error

		tags, err = crane.ListTags(repo,
			crane.WithAuthFromKeychain(gcrane.Keychain),
			crane.WithTransport(di.getTransport()),
		)
		if err != nil {
			return fmt.Errorf("listing tags for %s: %w", repo, err)
		}

		return nil
	}); err != nil {
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			return nil, nil
		}

		return nil, fmt.Errorf("with retry: %w", err)
	}

	return tags, nil
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
		crane.WithTransport(di.getTransport()),
	}

	logrus.Infof("SBOM copy: %s to %s", srcRefString, dstRefString)

	if err := ratelimit.WithRetry(func() error {
		return crane.Copy(srcRefString, dstRefString, craneOpts...)
	}); err != nil {
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

	if err := ratelimit.WithRetry(func() error {
		return remote.Write(ref, img,
			remote.WithAuthFromKeychain(gcrane.Keychain),
			remote.WithUserAgent(image.UserAgent),
			remote.WithTransport(di.getTransport()),
		)
	}); err != nil {
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
