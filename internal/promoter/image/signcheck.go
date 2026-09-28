/*
Copyright 2023 The Kubernetes Authors.

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
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/types"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	sgbundle "github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature/payload"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/types/known/timestamppb"

	"sigs.k8s.io/promo-tools/v4/image/consts"
	checkresults "sigs.k8s.io/promo-tools/v4/promoter/image/checkresults"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/registry"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	productionImagePath      = "k8s-artifacts-prod"
	productionRepositoryPath = productionImagePath + "/images"

	// canonicalRegistry is the primary Artifact Registry region used for
	// indexing images and as the signing target. This must match the
	// SIGNATURE_UPSTREAM_ENDPOINT configured in archeio so that signature
	// lookups via registry.k8s.io are routed to the correct backend.
	canonicalRegistry = "us-central1-docker.pkg.dev"

	// signCheckConcurrency is the number of images checked in parallel.
	signCheckConcurrency = 10

	// simpleSigningMediaType is the media type of cosign signature layers.
	simpleSigningMediaType = "application/vnd.dev.cosign.simplesigning.v1+json"

	// certificateAnnotation holds the signing certificate of a cosign
	// signature layer.
	certificateAnnotation = "dev.sigstore.cosign/certificate"
)

var (
	// metadataTagSuffixes are the suffixes of the tags cosign uses to
	// attach signatures, attestations and SBOMs to an image.
	metadataTagSuffixes = []string{".sig", ".att", ".sbom"}

	// sigstoreMediaTypePrefixes identify the sigstore bundles and cosign
	// artifacts attached to an image, by their artifact, config or layer
	// media type.
	sigstoreMediaTypePrefixes = []string{
		"application/vnd.dev.sigstore.",
		"application/vnd.dev.cosign.",
	}

	// attestationLayerMediaTypes are the layer media types of attestations
	// attached with the cosign tag convention (.att), whose manifests have
	// a plain image config.
	attestationLayerMediaTypes = []string{
		"application/vnd.dsse.envelope.v1+json",
		"application/vnd.in-toto+json",
	}

	// archSuffixes are the suffixes of the per architecture repositories.
	archSuffixes = []string{"-amd64", "-arm", "-arm64", "-ppc64le", "-s390x"}
)

// GetLatestImages returns the images to check: the ones passed as
// references, or the ones uploaded to the canonical registry within the
// configured date range.
func (di *DefaultPromoterImplementation) GetLatestImages(
	ctx context.Context, opts *options.Options,
) ([]checkresults.Image, error) {
	since, err := attestationsSince(opts)
	if err != nil {
		return nil, err
	}

	var images []checkresults.Image

	// If there is a list of images to check in the options
	// we default to checking those.
	if len(opts.SignCheckReferences) > 0 {
		images, err = di.referencedImages(ctx, opts.SignCheckReferences)
	} else {
		images, err = di.readLatestImages(ctx, opts, since)
	}

	if err != nil {
		return nil, fmt.Errorf("fetching latest images: %w", err)
	}

	images = applyAttestationsSince(images, since)

	if opts.SignCheckMaxImages > 0 && len(images) > opts.SignCheckMaxImages {
		images = images[:opts.SignCheckMaxImages]
	}

	logrus.Infof("Found %d images to check", len(images))

	return images, nil
}

// attestationsSince returns the time from which on promoted images must
// have a promotion attestation. It is zero when all images must have one.
func attestationsSince(opts *options.Options) (time.Time, error) {
	if opts.SignCheckAttestationsSince == "" {
		return time.Time{}, nil
	}

	since, err := time.Parse(time.DateOnly, opts.SignCheckAttestationsSince)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing attestations date %q: %w", opts.SignCheckAttestationsSince, err)
	}

	return since, nil
}

// applyAttestationsSince marks the images uploaded since the given time as
// requiring a promotion attestation. Older images are only checked for
// their signature, so the ones without a tag are dropped.
func applyAttestationsSince(images []checkresults.Image, since time.Time) []checkresults.Image {
	selected := make([]checkresults.Image, 0, len(images))

	for i := range images {
		img := images[i]
		img.Attest = !img.Uploaded.Before(since)

		if !img.Attest && len(img.Tags) == 0 {
			logrus.Debugf("Not checking %s, it was promoted before attestations", img.String())

			continue
		}

		selected = append(selected, img)
	}

	return selected
}

// signCheckRepository returns the production repository on the canonical
// registry, which holds the signatures and attestations of all promoted
// images.
func (di *DefaultPromoterImplementation) signCheckRepository() string {
	if di.signCheckRepo != "" {
		return di.signCheckRepo
	}

	return canonicalRegistry + "/" + productionRepositoryPath
}

// googleOptions returns the options to list the canonical registry.
func googleOptions(ctx context.Context) []google.Option {
	return []google.Option{
		google.WithAuthFromKeychain(authn.NewMultiKeychain(
			authn.DefaultKeychain,
			google.Keychain,
		)),
		google.WithContext(ctx),
		google.WithUserAgent(image.UserAgent),
	}
}

// referencedImages looks up the given references on the canonical registry.
func (di *DefaultPromoterImplementation) referencedImages(
	ctx context.Context, refs []string,
) ([]checkresults.Image, error) {
	images := make([]checkresults.Image, 0, len(refs))

	for _, refString := range refs {
		ref, err := name.ParseReference(refString)
		if err != nil {
			return nil, fmt.Errorf("invalid image reference %s: %w", refString, err)
		}

		imgName, err := productionImageName(ref)
		if err != nil {
			return nil, err
		}

		repo, err := name.NewRepository(di.signCheckRepository() + "/" + imgName)
		if err != nil {
			return nil, fmt.Errorf("creating repository for %s: %w", refString, err)
		}

		tags, err := google.List(repo, googleOptions(ctx)...)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", repo, err)
		}

		img, ok := imageForIdentifier(imgName, tags, ref.Identifier())
		if !ok {
			return nil, fmt.Errorf("%s not found in %s", refString, repo)
		}

		images = append(images, img)
	}

	return images, nil
}

// productionImageName returns the name of an image below the production
// repository, from its registry.k8s.io reference or its reference in an
// Artifact Registry region.
func productionImageName(ref name.Reference) (string, error) {
	repo := ref.Context()

	if repo.RegistryStr() == consts.ProdRegistry {
		return repo.RepositoryStr(), nil
	}

	if strings.HasSuffix(repo.RegistryStr(), artifactRegistryHostSuffix) {
		if imgName, ok := strings.CutPrefix(repo.RepositoryStr(), productionRepositoryPath+"/"); ok {
			return imgName, nil
		}
	}

	return "", fmt.Errorf("%s is not a production image reference", ref)
}

// imageForIdentifier returns the image with the given digest or tag from a
// repository listing.
func imageForIdentifier(imgName string, tags *google.Tags, identifier string) (checkresults.Image, bool) {
	for digest, info := range tags.Manifests {
		if digest == identifier || slices.Contains(info.Tags, identifier) {
			return checkresults.Image{
				Name: imgName, Digest: digest, Tags: sortedTags(info.Tags), Uploaded: info.Uploaded,
			}, true
		}
	}

	return checkresults.Image{}, false
}

// readLatestImages returns the images uploaded to the canonical registry
// in the configured date range. Note that this function uses the google
// GCR/AR extensions so it will not work on other non-GCP registries.
func (di *DefaultPromoterImplementation) readLatestImages(
	ctx context.Context, opts *options.Options, attestSince time.Time,
) ([]checkresults.Image, error) {
	now := time.Now()
	from := now.AddDate(0, 0, -opts.SignCheckFromDays)

	to := now
	if opts.SignCheckToDays > 0 {
		to = now.AddDate(0, 0, -opts.SignCheckToDays)
	}

	logrus.Infof("Checking images from %s to %s",
		from.Local().Format(time.RFC822), //nolint:gosmopolitan // local timezone is intentional for human-readable log output
		to.Local().Format(time.RFC822),   //nolint:gosmopolitan // local timezone is intentional for human-readable log output
	)

	root, err := name.NewRepository(di.signCheckRepository(), name.WeakValidation)
	if err != nil {
		return nil, fmt.Errorf("creating repo: %w", err)
	}

	var (
		images []checkresults.Image
		mu     sync.Mutex
	)

	walkFn := func(repo name.Repository, tags *google.Tags, err error) error {
		if err != nil {
			return fmt.Errorf("listing %s: %w", repo, err)
		}

		if tags == nil {
			return nil
		}

		imgName, ok := strings.CutPrefix(repo.RepositoryStr(), productionRepositoryPath+"/")
		if !ok {
			return nil
		}

		// We ignore the -arch repositories as the promoter currently
		// ignores them and does not sign them
		for _, suffix := range archSuffixes {
			if strings.HasSuffix(imgName, suffix) {
				return nil
			}
		}

		logrus.Infof("Indexing %d images from %s", len(tags.Manifests), repo)

		selected, err := di.selectImages(ctx, imgName, tags, from, to, attestSince)
		if err != nil {
			return err
		}

		mu.Lock()

		images = append(images, selected...)
		mu.Unlock()

		return nil
	}

	if err := google.Walk(root, walkFn, googleOptions(ctx)...); err != nil {
		return nil, fmt.Errorf("walking repo: %w", err)
	}

	sort.Slice(images, func(i, j int) bool {
		if images[i].Name != images[j].Name {
			return images[i].Name < images[j].Name
		}

		return images[i].Digest < images[j].Digest
	})

	return images, nil
}

// selectImages returns the images of a repository listing uploaded
// between from and to.
//
// Signatures, attestations and other artifacts attached to an image are
// not images themselves and are skipped. Digests without a tag are only
// checked for their attestation, so they are only selected when uploaded
// since attestSince and when they are not the child of an index, because
// promotion attests them only when they are listed in a manifest
// themselves.
func (di *DefaultPromoterImplementation) selectImages(
	ctx context.Context, imgName string, tags *google.Tags, from, to, attestSince time.Time,
) ([]checkresults.Image, error) {
	var (
		images   []checkresults.Image
		tagless  []string
		indexes  []string
		inWindow = func(t time.Time) bool {
			return !t.Before(from) && !t.After(to)
		}
	)

	for digest, info := range tags.Manifests {
		// The children of an index are uploaded before it, so indexes
		// up to now are needed to find them. A child in the window whose
		// index was uploaded before from is not recognized and gets
		// checked on its own, which promotion order makes unlikely.
		if types.MediaType(info.MediaType).IsIndex() && !info.Uploaded.Before(from) {
			indexes = append(indexes, digest)
		}

		if !inWindow(info.Uploaded) || isMetadataTag(info.Tags) {
			continue
		}

		if len(info.Tags) == 0 {
			if !info.Uploaded.Before(attestSince) {
				tagless = append(tagless, digest)
			}

			continue
		}

		images = append(images, checkresults.Image{
			Name: imgName, Digest: digest, Tags: sortedTags(info.Tags), Uploaded: info.Uploaded,
		})
	}

	if len(tagless) == 0 {
		return images, nil
	}

	repo := di.signCheckRepository() + "/" + imgName

	children := map[string]struct{}{}

	for _, digest := range indexes {
		raw, err := di.getManifest(ctx, repo+"@"+digest)
		if err != nil {
			return nil, err
		}

		idx, err := v1.ParseIndexManifest(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("parsing index %s@%s: %w", repo, digest, err)
		}

		for i := range idx.Manifests {
			children[idx.Manifests[i].Digest.String()] = struct{}{}
		}
	}

	for _, digest := range tagless {
		if _, ok := children[digest]; ok {
			continue
		}

		if !types.MediaType(tags.Manifests[digest].MediaType).IsIndex() {
			raw, err := di.getManifest(ctx, repo+"@"+digest)
			if err != nil {
				return nil, err
			}

			if isSigstoreArtifact(raw) {
				continue
			}
		}

		images = append(images, checkresults.Image{
			Name: imgName, Digest: digest, Uploaded: tags.Manifests[digest].Uploaded,
		})
	}

	return images, nil
}

// getManifest returns the raw manifest of a reference.
func (di *DefaultPromoterImplementation) getManifest(ctx context.Context, refString string) ([]byte, error) {
	ref, err := name.ParseReference(refString)
	if err != nil {
		return nil, fmt.Errorf("parsing reference %s: %w", refString, err)
	}

	desc, err := remote.Get(ref, append(di.remoteOptions(), remote.WithContext(ctx))...)
	if err != nil {
		return nil, fmt.Errorf("getting manifest of %s: %w", refString, err)
	}

	return desc.Manifest, nil
}

// isMetadataTag reports whether one of the tags is a cosign signature,
// attestation or SBOM tag.
func isMetadataTag(tags []string) bool {
	for _, tag := range tags {
		for _, suffix := range metadataTagSuffixes {
			if strings.HasSuffix(tag, suffix) {
				return true
			}
		}
	}

	return false
}

// isSigstoreArtifact reports whether a manifest holds a sigstore bundle, a
// cosign signature or an attestation instead of an image.
//
// Cosign signature (.sig) and attestation (.att) manifests have a plain
// image config and are only recognized by their layers. They matter even
// without a tag: promotion moves the signature tag when it signs on top of
// the copied staging signature, which leaves the previous signature
// manifest behind without a tag.
func isSigstoreArtifact(raw []byte) bool {
	var manifest struct {
		ArtifactType string `json:"artifactType"`
		Config       struct {
			MediaType string `json:"mediaType"`
		} `json:"config"`
		Layers []struct {
			MediaType string `json:"mediaType"`
		} `json:"layers"`
	}

	if err := json.Unmarshal(raw, &manifest); err != nil {
		return false
	}

	mediaTypes := []string{manifest.ArtifactType, manifest.Config.MediaType}
	for i := range manifest.Layers {
		mediaTypes = append(mediaTypes, manifest.Layers[i].MediaType)
	}

	for _, mediaType := range mediaTypes {
		if slices.Contains(attestationLayerMediaTypes, mediaType) {
			return true
		}

		for _, prefix := range sigstoreMediaTypePrefixes {
			if strings.HasPrefix(mediaType, prefix) {
				return true
			}
		}
	}

	return false
}

// sortedTags returns a sorted copy of the tags.
func sortedTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}

	sorted := slices.Clone(tags)
	sort.Strings(sorted)

	return sorted
}

// signCheckSAN returns the expected subject alternative name and its
// regular expression. As in promotion, the regular expression takes
// precedence over the exact value, so only one of them is set.
func signCheckSAN(opts *options.Options) (string, string) {
	if opts.SignCheckIdentityRegexp != "" {
		return "", opts.SignCheckIdentityRegexp
	}

	return opts.SignCheckIdentity, ""
}

// signCheckIdentity returns the certificate identity signatures and
// attestations must have. As in promotion, the regular expressions take
// precedence over the exact values.
func signCheckIdentity(opts *options.Options) (*verify.CertificateIdentity, error) {
	san, sanRegexp := signCheckSAN(opts)

	issuer, issuerRegexp := opts.SignCheckIssuer, ""
	if opts.SignCheckIssuerRegexp != "" {
		issuer, issuerRegexp = "", opts.SignCheckIssuerRegexp
	}

	identity, err := verify.NewShortCertificateIdentity(issuer, issuerRegexp, san, sanRegexp)
	if err != nil {
		return nil, fmt.Errorf("creating certificate identity: %w", err)
	}

	return &identity, nil
}

// checkSignerAccount ensures that --signer-account signs with an identity
// sigcheck accepts. Otherwise every run would sign and attest the images
// again without repairing them.
func checkSignerAccount(opts *options.Options) error {
	matcher, err := verify.NewSANMatcher(signCheckSAN(opts))
	if err != nil {
		return fmt.Errorf("creating identity matcher: %w", err)
	}

	if err := matcher.Verify(certificate.Summary{SubjectAlternativeName: opts.SignerAccount}); err != nil {
		return fmt.Errorf("signer account %q does not match the certificate identity: %w", opts.SignerAccount, err)
	}

	return nil
}

// matchesIdentity reports whether a certificate has the expected identity.
func matchesIdentity(identity *verify.CertificateIdentity, cert *x509.Certificate) bool {
	summary, err := certificate.SummarizeCertificate(cert)
	if err != nil {
		logrus.Debugf("Unable to read certificate: %v", err)

		return false
	}

	if err := identity.Verify(summary); err != nil {
		logrus.Debugf("Certificate does not match: %v", err)

		return false
	}

	return true
}

// signCheckEdges returns the edges promoting an image to the canonical
// registry, one per tag, so that sigcheck signs and attests with the same
// identities and at the same place as promotion.
func (di *DefaultPromoterImplementation) signCheckEdges(img *checkresults.Image) []promotion.Edge {
	tags := img.Tags
	if len(tags) == 0 {
		tags = []string{""}
	}

	edges := make([]promotion.Edge, 0, len(tags))
	for _, tag := range tags {
		edges = append(edges, promotion.Edge{
			Digest:      image.Digest(img.Digest),
			DstRegistry: registry.Context{Name: image.Registry(di.signCheckRepository())},
			DstImageTag: promotion.ImageTag{Name: image.Name(img.Name), Tag: image.Tag(tag)},
		})
	}

	return edges
}

// GetSignatureStatus checks the signature and the promotion attestation of
// each image on the canonical registry.
func (di *DefaultPromoterImplementation) GetSignatureStatus(
	ctx context.Context, opts *options.Options, images []checkresults.Image,
) (checkresults.Results, error) {
	identity, err := signCheckIdentity(opts)
	if err != nil {
		return nil, err
	}

	results := make(checkresults.Results, len(images))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(signCheckConcurrency)

	for i := range images {
		g.Go(func() error {
			status, err := di.imageStatus(ctx, identity, &images[i])
			if err != nil {
				return fmt.Errorf("checking %s: %w", images[i].String(), err)
			}

			results[i] = status

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("checking images: %w", err)
	}

	return results, nil
}

// imageStatus checks the signature and the promotion attestation of an
// image. Signatures are only checked for images with tags, because
// promotion does not sign digests promoted without a tag, and attestations
// only for images promoted since promotion writes them.
func (di *DefaultPromoterImplementation) imageStatus(
	ctx context.Context, identity *verify.CertificateIdentity, img *checkresults.Image,
) (checkresults.Status, error) {
	status := checkresults.Status{Image: *img}
	edge := di.signCheckEdges(img)[0]

	if len(img.Tags) > 0 {
		signed, err := di.hasSignature(ctx, identity, &edge)
		if err != nil {
			return status, err
		}

		status.Signed = signed
		if !signed {
			logrus.Warnf("No valid signature found for %s", img.String())
		}
	}

	if !img.Attest {
		return status, nil
	}

	attested, err := di.hasAttestation(identity, &edge)
	if err != nil {
		return status, err
	}

	status.Attested = attested
	if !attested {
		logrus.Warnf("No valid promotion attestation found for %s", img.String())
	}

	return status, nil
}

// hasSignature reports whether the signature tag of an edge on the
// canonical registry holds a signature of the expected identity for the
// production reference of the image.
func (di *DefaultPromoterImplementation) hasSignature(
	ctx context.Context, identity *verify.CertificateIdentity, edge *promotion.Edge,
) (bool, error) {
	digest, err := name.NewDigest(canonicalDigestRef(edge))
	if err != nil {
		return false, fmt.Errorf("parsing digest reference: %w", err)
	}

	sigRef := digest.Context().Tag(digestToSignatureTag(edge.Digest))

	sigImage, err := remote.Image(sigRef, append(di.remoteOptions(), remote.WithContext(ctx))...)
	if err != nil {
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			return false, nil
		}

		return false, fmt.Errorf("reading signature %s: %w", sigRef, err)
	}

	manifest, err := sigImage.Manifest()
	if err != nil {
		return false, fmt.Errorf("reading signature manifest %s: %w", sigRef, err)
	}

	for i := range manifest.Layers {
		layer := &manifest.Layers[i]
		if layer.MediaType != simpleSigningMediaType {
			continue
		}

		certs, err := cryptoutils.UnmarshalCertificatesFromPEM(
			[]byte(layer.Annotations[certificateAnnotation]),
		)
		if err != nil || len(certs) == 0 || !matchesIdentity(identity, certs[0]) {
			continue
		}

		matches, err := signedPayloadMatches(sigImage, layer.Digest, targetIdentity(edge), string(edge.Digest))
		if err != nil {
			return false, fmt.Errorf("reading signature payload of %s: %w", sigRef, err)
		}

		if matches {
			return true, nil
		}
	}

	return false, nil
}

// signedPayloadMatches reports whether a signature layer signs the given
// production reference and digest.
func signedPayloadMatches(sigImage v1.Image, layerDigest v1.Hash, identity, digest string) (bool, error) {
	layer, err := sigImage.LayerByDigest(layerDigest)
	if err != nil {
		return false, fmt.Errorf("getting layer %s: %w", layerDigest, err)
	}

	// Uncompressed passes the plain payloads cosign writes through as they
	// are and also handles compressed ones.
	rc, err := layer.Uncompressed()
	if err != nil {
		return false, fmt.Errorf("reading layer %s: %w", layerDigest, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return false, fmt.Errorf("reading layer %s: %w", layerDigest, err)
	}

	var signed payload.SimpleContainerImage
	if err := json.Unmarshal(data, &signed); err != nil {
		logrus.Debugf("Unable to parse signature payload %s: %v", layerDigest, err)

		return false, nil
	}

	return signed.Critical.Identity.DockerReference == identity &&
		signed.Critical.Image.DockerManifestDigest == digest, nil
}

// hasAttestation reports whether the digest of an edge on the canonical
// registry has a promotion attestation of the expected identity for the
// production reference of the image.
func (di *DefaultPromoterImplementation) hasAttestation(
	identity *verify.CertificateIdentity, edge *promotion.Edge,
) (bool, error) {
	digest, err := name.NewDigest(canonicalDigestRef(edge))
	if err != nil {
		return false, fmt.Errorf("parsing digest reference: %w", err)
	}

	refs, err := di.bundleReferrers(digest, provenance.PredicateType)
	if err != nil {
		return false, err
	}

	for _, ref := range refs {
		bundle, err := ociremote.Bundle(ref, ociremote.WithRemoteOptions(di.remoteOptions()...))
		if err != nil {
			logrus.Debugf("Unable to read attestation bundle %s: %v", ref, err)

			continue
		}

		if attestationMatches(bundle, identity, targetIdentity(edge), string(edge.Digest)) {
			return true, nil
		}
	}

	return false, nil
}

// attestationMatches reports whether a bundle holds a promotion statement
// about the given production reference and digest, signed by the expected
// identity.
func attestationMatches(
	bundle *sgbundle.Bundle, identity *verify.CertificateIdentity, subject, digest string,
) bool {
	content, err := bundle.VerificationContent()
	if err != nil || content.Certificate() == nil || !matchesIdentity(identity, content.Certificate()) {
		return false
	}

	envelope, err := bundle.Envelope()
	if err != nil {
		return false
	}

	statement, err := envelope.Statement()
	if err != nil || statement.GetPredicateType() != provenance.PredicateType {
		return false
	}

	algorithm, value, ok := strings.Cut(digest, ":")
	if !ok {
		return false
	}

	for _, s := range statement.GetSubject() {
		if s.GetName() == subject && s.GetDigest()[algorithm] == value {
			return true
		}
	}

	return false
}

// FixMissingSignatures signs the unsigned images on the canonical registry
// with their production reference, like promotion does.
func (di *DefaultPromoterImplementation) FixMissingSignatures(
	_ context.Context, opts *options.Options, results checkresults.Results,
) error {
	unsigned := results.Unsigned()
	if len(unsigned) == 0 {
		return nil
	}

	if err := checkSignerAccount(opts); err != nil {
		return err
	}

	logrus.Infof("Signing %d images", len(unsigned))

	//nolint:contextcheck
	signOpts, err := di.initSigner(opts)
	if err != nil {
		return err
	}

	g := new(errgroup.Group)
	g.SetLimit(opts.MaxSignatureOps)

	for i := range unsigned {
		edge := di.signCheckEdges(&unsigned[i].Image)[0]

		g.Go(func() error {
			return di.signWithIdentity(signOpts, targetIdentity(&edge), canonicalDigestRef(&edge))
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("signing images: %w", err)
	}

	return nil
}

// FixMissingAttestations writes the promotion attestations of the
// unattested images to the canonical registry, like promotion does. The
// staging image and the manifest are not known here, so the records do
// not include them.
func (di *DefaultPromoterImplementation) FixMissingAttestations(
	ctx context.Context,
	opts *options.Options,
	results checkresults.Results,
	generator provenance.Generator,
) error {
	unattested := results.Unattested()
	if len(unattested) == 0 {
		return nil
	}

	if err := checkSignerAccount(opts); err != nil {
		return err
	}

	logrus.Infof("Attesting %d images", len(unattested))

	//nolint:contextcheck
	if err := di.ensureAttestationSigner(opts); err != nil {
		return fmt.Errorf("initializing attestation signer: %w", err)
	}

	builderID := promotionBuilderID()
	now := time.Now()
	recordContext := newRecordContext(nil)

	g := new(errgroup.Group)
	g.SetLimit(opts.MaxSignatureOps)

	for i := range unattested {
		group := di.signCheckEdges(&unattested[i].Image)
		identity := targetIdentity(&group[0])

		record := recordContext.record(group, identity)
		record.Timestamp = timestamppb.New(now)
		record.BuilderId = builderID

		digest, err := name.NewDigest(canonicalDigestRef(&group[0]))
		if err != nil {
			return fmt.Errorf("parsing digest reference: %w", err)
		}

		g.Go(func() error {
			if err := di.writeAttestation(ctx, digest, generator, record); err != nil {
				return fmt.Errorf("writing provenance for %s: %w", identity, err)
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("writing provenance attestations: %w", err)
	}

	return nil
}
