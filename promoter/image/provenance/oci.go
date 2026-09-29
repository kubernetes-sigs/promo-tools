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

package provenance

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/carabiner-dev/attestation"
	"github.com/carabiner-dev/collector/envelope/bare"
	"github.com/carabiner-dev/collector/envelope/bundle"
	"github.com/carabiner-dev/collector/repository/coci"
	"github.com/carabiner-dev/signer"
	sapi "github.com/carabiner-dev/signer/api/v1"
	signeroptions "github.com/carabiner-dev/signer/options"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/gcrane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	sgbundle "github.com/sigstore/sigstore-go/pkg/bundle"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	// BundleArtifactType is the artifact type of the sigstore bundles that
	// cosign v3 attaches as OCI referrers, for signatures and attestations
	// alike.
	BundleArtifactType = "application/vnd.dev.sigstore.bundle.v0.3+json"

	// dsseMediaType is the media type of the DSSE envelope layers of legacy
	// cosign `.att` tags.
	dsseMediaType = "application/vnd.dsse.envelope.v1+json"

	// inTotoMediaType is the media type of unsigned in-toto statements in
	// `.att` tags, as written by kpromo v4.3.1 pre-releases.
	inTotoMediaType = "application/vnd.in-toto+json"

	// referrerConcurrency is the number of referrers of an image that are
	// read in parallel.
	referrerConcurrency = 4

	// dockerReferenceTypeAnnotation and dockerAttestationManifest mark the
	// attestation manifests BuildKit adds to an index, with the platform
	// unknownPlatform/unknownPlatform.
	dockerReferenceTypeAnnotation = "vnd.docker.reference.type"
	dockerAttestationManifest     = "attestation-manifest"
	unknownPlatform               = "unknown"

	// maxLayerSize limits how much of an attestation layer is read.
	// Bundles are a few KiB; SBOMs attested as bundles can be larger.
	maxLayerSize = 64 << 20
)

// OCIDiscoverer finds the attestations of an image in its registry: sigstore
// bundles attached as OCI referrers and DSSE envelopes in legacy cosign
// `.att` tags. For an index, the referrers and `.att` tags of its direct
// children in the same repository are included, because builders attach
// attestations to the platform images.
//
// Attestations are verified cryptographically against the sigstore trust
// roots of carabiner-dev/signer, without checking who signed them. Which
// signers are trusted, and whether an attestation must be about the image
// (Attestation.SubjectMatches), is up to the caller.
type OCIDiscoverer struct {
	// Transport is the HTTP transport used for registry requests. Set it
	// to the promoter transport so that discovery shares the global rate
	// limit. Defaults to the go-containerregistry default transport.
	Transport http.RoundTripper

	// verifyFn verifies the signature of an envelope. It exists so that
	// tests don't reach the sigstore trust root.
	verifyFn func(attestation.Envelope) error

	// verifier verifies sigstore bundles. Creating one loads the trust
	// roots, which takes far longer than a verification, so it is shared
	// and used by one goroutine at a time.
	verifierOnce sync.Once
	verifierMu   sync.Mutex
	verifier     *signer.Verifier
}

// Discover implements Discoverer.
func (d *OCIDiscoverer) Discover(ctx context.Context, ref string) (*Discovery, error) {
	digest, err := name.NewDigest(ref)
	if err != nil {
		return nil, fmt.Errorf("parsing digest reference %q: %w", ref, err)
	}

	// Share one puller, so the registry token is fetched only once.
	puller, err := remote.NewPuller(d.remoteOptions(ctx)...)
	if err != nil {
		return nil, fmt.Errorf("creating puller: %w", err)
	}

	opts := append(d.remoteOptions(ctx), remote.Reuse(puller))

	digests, err := subjects(digest, opts)
	if err != nil {
		return nil, err
	}

	discovery := &Discovery{Reference: ref}

	for _, child := range digests[1:] {
		discovery.Children = append(discovery.Children, child.DigestStr())
	}

	for _, dg := range digests {
		if err := d.discoverReferrers(dg, opts, discovery); err != nil {
			return nil, err
		}

		if err := d.discoverAttestationTag(ctx, dg, opts, discovery); err != nil {
			return nil, err
		}
	}

	return discovery, nil
}

// IsPlatformManifest reports whether a child of an index is a platform
// manifest: an image or an index that is not one of the attestation
// manifests BuildKit adds. Those are marked by an annotation and have the
// platform unknown/unknown, which no runtime pulls; an annotated child with
// any other platform still counts, so that the annotation can't hide a
// platform image from the provenance policies.
func IsPlatformManifest(desc *v1.Descriptor) bool {
	if !desc.MediaType.IsImage() && !desc.MediaType.IsIndex() {
		return false
	}

	return desc.Annotations[dockerReferenceTypeAnnotation] != dockerAttestationManifest ||
		desc.Platform == nil ||
		desc.Platform.OS != unknownPlatform ||
		desc.Platform.Architecture != unknownPlatform
}

// subjects returns the digest and, for an index, the digests of its
// platform manifests (IsPlatformManifest).
func subjects(digest name.Digest, opts []remote.Option) ([]name.Digest, error) {
	desc, err := remote.Get(digest, opts...)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest of %s: %w", digest, err)
	}

	digests := []name.Digest{digest}

	if !desc.MediaType.IsIndex() {
		return digests, nil
	}

	idx, err := desc.ImageIndex()
	if err != nil {
		return nil, fmt.Errorf("reading index %s: %w", digest, err)
	}

	im, err := idx.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("reading index manifest %s: %w", digest, err)
	}

	for i := range im.Manifests {
		child := &im.Manifests[i]
		if IsPlatformManifest(child) {
			digests = append(digests, digest.Context().Digest(child.Digest.String()))
		}
	}

	return digests, nil
}

// discoverReferrers adds the sigstore bundles attached to the digest as
// referrers to the discovery, and lists the other referrers. Only a failure
// to list the referrers is an error; a referrer that can't be read is
// recorded in the discovery and skipped.
func (d *OCIDiscoverer) discoverReferrers(
	digest name.Digest, opts []remote.Option, discovery *Discovery,
) error {
	idx, err := remote.Referrers(digest, opts...)
	if err != nil {
		return fmt.Errorf("listing referrers of %s: %w", digest, err)
	}

	im, err := idx.IndexManifest()
	if err != nil {
		return fmt.Errorf("reading referrers of %s: %w", digest, err)
	}

	// Referrers are read in parallel, each into its own discovery, which
	// are merged in the order of the referrers list.
	found := make([]Discovery, len(im.Manifests))

	var wg sync.WaitGroup

	sem := make(chan struct{}, referrerConcurrency)

	for i := range im.Manifests {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			desc := &im.Manifests[i]
			if err := d.discoverReferrer(digest, desc, opts, &found[i]); err != nil {
				found[i].Errors = append(found[i].Errors,
					fmt.Sprintf("%s referrer %s: %v", digest.DigestStr(), desc.Digest, err))
			}
		})
	}

	wg.Wait()

	for i := range found {
		discovery.Attestations = append(discovery.Attestations, found[i].Attestations...)
		discovery.Referrers = append(discovery.Referrers, found[i].Referrers...)
		discovery.Errors = append(discovery.Errors, found[i].Errors...)
	}

	return nil
}

// discoverReferrer adds the sigstore bundles of one referrer to the
// discovery, or lists the referrer if it holds none.
func (d *OCIDiscoverer) discoverReferrer(
	digest name.Digest, desc *v1.Descriptor, opts []remote.Option, discovery *Discovery,
) error {
	rd, err := remote.Get(digest.Context().Digest(desc.Digest.String()), opts...)
	if err != nil {
		return fmt.Errorf("fetching manifest: %w", err)
	}

	// Bundles are image manifests, anything else is listed as is.
	if !rd.MediaType.IsImage() {
		var m struct {
			ArtifactType string `json:"artifactType"`
		}

		// An unparseable manifest is still listed, by its media type.
		_ = json.Unmarshal(rd.Manifest, &m) //nolint:errcheck // see above

		discovery.Referrers = append(discovery.Referrers, Referrer{
			Digest:       digest.DigestStr(),
			Location:     desc.Digest.String(),
			ArtifactType: cmp.Or(m.ArtifactType, desc.ArtifactType, string(rd.MediaType)),
		})

		return nil
	}

	img, err := rd.Image()
	if err != nil {
		return fmt.Errorf("reading image: %w", err)
	}

	mf, err := img.Manifest()
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}

	bundles := bundleLayers(desc, mf)
	if len(bundles) == 0 {
		discovery.Referrers = append(discovery.Referrers, Referrer{
			Digest:       digest.DigestStr(),
			Location:     desc.Digest.String(),
			ArtifactType: artifactType(desc, mf),
		})

		return nil
	}

	var errs []error

	for _, layer := range bundles {
		envs, err := readBundle(img, layer)
		if err != nil {
			errs = append(errs, fmt.Errorf("bundle %s: %w", layer, err))

			continue
		}

		for _, env := range envs {
			att := d.newAttestation(digest.DigestStr(), SourceReferrer, desc.Digest.String(), env)
			att.Layer = layer.String()
			discovery.Attestations = append(discovery.Attestations, att)
		}
	}

	return errors.Join(errs...)
}

// bundleLayers returns the digests of the layers of a referrer that hold
// sigstore bundles. Registries don't always copy the artifact type into the
// referrers list, so the manifest and its layer media types are checked as
// well.
func bundleLayers(desc *v1.Descriptor, mf *v1.Manifest) []v1.Hash {
	isBundle := desc.ArtifactType == BundleArtifactType ||
		mf.ArtifactType == BundleArtifactType

	var layers []v1.Hash

	for i := range mf.Layers {
		if isBundle || string(mf.Layers[i].MediaType) == BundleArtifactType {
			layers = append(layers, mf.Layers[i].Digest)
		}
	}

	return layers
}

// artifactType returns the artifact type of a referrer. The manifest comes
// first, because some registries fill the referrers list with the config
// media type instead.
func artifactType(desc *v1.Descriptor, mf *v1.Manifest) string {
	switch {
	case mf.ArtifactType != "":
		return mf.ArtifactType
	case desc.ArtifactType != "":
		return desc.ArtifactType
	default:
		return string(mf.Config.MediaType)
	}
}

// readBundle reads and parses one sigstore bundle layer.
func readBundle(img v1.Image, layer v1.Hash) ([]attestation.Envelope, error) {
	data, err := readLayer(img, layer)
	if err != nil {
		return nil, err
	}

	envs, err := (&bundle.Parser{}).Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parsing bundle: %w", err)
	}

	return envs, nil
}

// readLayer reads a layer of at most maxLayerSize bytes.
func readLayer(img v1.Image, layer v1.Hash) ([]byte, error) {
	l, err := img.LayerByDigest(layer)
	if err != nil {
		return nil, fmt.Errorf("getting layer: %w", err)
	}

	rc, err := l.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("reading layer: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, maxLayerSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading layer: %w", err)
	}

	if len(data) > maxLayerSize {
		return nil, fmt.Errorf("layer is larger than %d bytes", maxLayerSize)
	}

	return data, nil
}

// discoverAttestationTag adds the attestations in the legacy cosign `.att`
// tag of the digest to the discovery: DSSE envelopes, and unsigned in-toto
// statements. A missing tag is not an error, and layers that can't be read
// are recorded in the discovery and skipped.
func (d *OCIDiscoverer) discoverAttestationTag(
	ctx context.Context, digest name.Digest, opts []remote.Option, discovery *Discovery,
) error {
	tag := digestToAttestationTag(image.Digest(digest.DigestStr()))

	img, err := remote.Image(digest.Context().Tag(tag), opts...)
	if err != nil {
		var terr *transport.Error
		if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
			return nil
		}

		return fmt.Errorf("fetching attestation tag of %s: %w", digest, err)
	}

	mf, err := img.Manifest()
	if err != nil {
		return fmt.Errorf("reading attestation tag manifest of %s: %w", digest, err)
	}

	hasDSSE := false

	for i := range mf.Layers {
		switch string(mf.Layers[i].MediaType) {
		case dsseMediaType:
			hasDSSE = true
		case inTotoMediaType:
			envs, err := readStatement(img, mf.Layers[i].Digest)
			if err != nil {
				discovery.Errors = append(discovery.Errors,
					fmt.Sprintf("%s %s layer %s: %v", digest.DigestStr(), tag, mf.Layers[i].Digest, err))

				continue
			}

			for _, env := range envs {
				discovery.Attestations = append(discovery.Attestations, d.newAttestation(
					digest.DigestStr(), SourceAttestationTag, tag, env,
				))
			}
		}
	}

	if !hasDSSE {
		return nil
	}

	// The collector turns DSSE layers with cosign's certificate and
	// transparency log annotations into sigstore bundles. It skips layers
	// it can't parse.
	collector, err := coci.New(
		coci.WithReference(digest.String()),
		coci.WithCraneOpts(d.craneOptions(ctx)...),
		coci.WithReadSignatures(false),
		coci.WithReadSBOMs(false),
	)
	if err != nil {
		return fmt.Errorf("creating attestation tag collector: %w", err)
	}

	envs, err := collector.Fetch(ctx, attestation.FetchOptions{MaxReadSize: maxLayerSize})
	if err != nil {
		discovery.Errors = append(discovery.Errors,
			fmt.Sprintf("%s %s: %v", digest.DigestStr(), tag, err))

		return nil
	}

	for _, env := range envs {
		discovery.Attestations = append(discovery.Attestations, d.newAttestation(
			digest.DigestStr(), SourceAttestationTag, tag, env,
		))
	}

	return nil
}

// readStatement reads and parses an unsigned in-toto statement layer.
func readStatement(img v1.Image, layer v1.Hash) ([]attestation.Envelope, error) {
	data, err := readLayer(img, layer)
	if err != nil {
		return nil, err
	}

	envs, err := bare.New().ParseStream(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parsing statement: %w", err)
	}

	return envs, nil
}

// newAttestation verifies the signature of an envelope and describes it.
func (d *OCIDiscoverer) newAttestation(
	dg string, source Source, location string, env attestation.Envelope,
) Attestation {
	att := Attestation{
		Digest:   dg,
		Source:   source,
		Location: location,
		Envelope: env,
	}

	if st := env.GetStatement(); st != nil {
		att.PredicateType = string(st.GetPredicateType())
		att.SubjectMatches = hasSubject(st, dg)
	}

	if err := d.verify(env); err != nil {
		att.Status = SignatureUnverifiable
		att.Error = err.Error()

		return att
	}

	att.Status, att.Signers, att.Error = verificationResult(env)

	return att
}

// hasSubject reports whether a subject of the statement has the digest.
func hasSubject(st attestation.Statement, dg string) bool {
	algorithm, hex, ok := strings.Cut(dg, ":")
	if !ok {
		return false
	}

	for _, subject := range st.GetSubjects() {
		if subject != nil && subject.GetDigest()[algorithm] == hex {
			return true
		}
	}

	return false
}

// verify runs verifyFn, or verifies the envelope against the sigstore trust
// roots.
func (d *OCIDiscoverer) verify(env attestation.Envelope) error {
	if d.verifyFn != nil {
		return d.verifyFn(env)
	}

	if b, ok := env.(*bundle.Envelope); ok {
		return d.verifyBundle(b)
	}

	//nolint:wrapcheck // the caller records the message
	return env.Verify()
}

// verifyBundle does what bundle.Envelope.Verify does, with the shared
// verifier: it verifies the signatures without an identity check and
// records the outcome in the predicate.
func (d *OCIDiscoverer) verifyBundle(b *bundle.Envelope) error {
	pred := b.GetPredicate()
	if pred == nil {
		return errors.New("bundle has no predicate")
	}

	d.verifierOnce.Do(func() {
		d.verifier = signer.NewVerifier()
	})

	d.verifierMu.Lock()
	defer d.verifierMu.Unlock()

	verification, err := d.verifier.VerifyStatement(
		&signer.BundleArtifact{Bundle: &sgbundle.Bundle{Bundle: &b.Bundle}},
		signeroptions.WithSkipIdentityCheck(true),
	)
	if err != nil {
		return fmt.Errorf("verifying sigstore signatures: %w", err)
	}

	pred.SetVerification(verification)

	return nil
}

// verificationResult reads the outcome the envelope recorded when it was
// verified. An envelope that recorded nothing is only reported as unsigned
// when it carries no signatures.
func verificationResult(env attestation.Envelope) (SignatureStatus, []string, string) {
	var sig *sapi.SignatureVerification

	if v, ok := env.GetVerification().(interface {
		GetSignature() *sapi.SignatureVerification
	}); ok {
		sig = v.GetSignature()
	}

	switch sig.GetStatus() {
	case sapi.VerificationStatus_VERIFIED:
		if !sig.GetVerified() {
			return SignatureFailed, nil, "verification recorded without success"
		}

		var signers []string

		for _, id := range sig.GetIdentities() {
			if p := id.Principal(); p != "" && !slices.Contains(signers, p) {
				signers = append(signers, p)
			}
		}

		return SignatureVerified, signers, ""
	case sapi.VerificationStatus_FAILED:
		return SignatureFailed, nil, sig.GetError()
	case sapi.VerificationStatus_UNVERIFIABLE:
		return SignatureUnverifiable, nil, sig.GetError()
	case sapi.VerificationStatus_UNSIGNED:
		return SignatureUnsigned, nil, ""
	case sapi.VerificationStatus_UNSPECIFIED:
	}

	if len(env.GetSignatures()) == 0 {
		return SignatureUnsigned, nil, ""
	}

	return SignatureUnverifiable, nil, "no verification result recorded"
}

// Summary returns one line per attestation, referrer and error, for
// logging.
func (d *Discovery) Summary() []string {
	lines := make([]string, 0, len(d.Attestations)+len(d.Referrers)+len(d.Errors))

	for i := range d.Attestations {
		a := &d.Attestations[i]

		status := string(a.Status)
		switch {
		case len(a.Signers) > 0:
			status += " by " + strings.Join(a.Signers, ", ")
		case a.Error != "":
			status += ": " + a.Error
		}

		if !a.SubjectMatches {
			status += "; the statement is not about " + a.Digest
		}

		lines = append(lines, fmt.Sprintf(
			"%s %s %s: %s (%s)", a.Digest, a.Source, a.Location, a.PredicateType, status,
		))
	}

	for _, r := range d.Referrers {
		lines = append(lines, fmt.Sprintf(
			"%s referrer %s: %s (no attestation)", r.Digest, r.Location, r.ArtifactType,
		))
	}

	for _, err := range d.Errors {
		lines = append(lines, "skipped "+err)
	}

	return lines
}

// remoteOptions returns the go-containerregistry options for requests.
func (d *OCIDiscoverer) remoteOptions(ctx context.Context) []remote.Option {
	opts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(gcrane.Keychain),
		remote.WithUserAgent(image.UserAgent),
	}

	if d.Transport != nil {
		opts = append(opts, remote.WithTransport(d.Transport))
	}

	return opts
}

// craneOptions returns the crane options for the `.att` tag collector.
func (d *OCIDiscoverer) craneOptions(ctx context.Context) []crane.Option {
	opts := []crane.Option{
		crane.WithContext(ctx),
		crane.WithAuthFromKeychain(gcrane.Keychain),
		crane.WithUserAgent(image.UserAgent),
	}

	if d.Transport != nil {
		opts = append(opts, crane.WithTransport(d.Transport))
	}

	return opts
}
