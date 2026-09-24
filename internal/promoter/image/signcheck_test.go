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
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	sgsign "github.com/sigstore/sigstore-go/pkg/sign"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature/payload"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	checkresults "sigs.k8s.io/promo-tools/v4/promoter/image/checkresults"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	testSignerIdentity = "krel-trust@k8s-releng-prod.iam.gserviceaccount.com"
	testOtherIdentity  = "someone@example.com"
	testIssuer         = "https://accounts.google.com"
	testTagStable      = "stable"

	testOtherIdentityRegexp = `^someone@example\.com$`
)

// testSignCheckOptions returns the options sigcheck runs with by default.
func testSignCheckOptions() *options.Options {
	return &options.Options{
		SignerAccount:     testSignerIdentity,
		SignCheckIdentity: testSignerIdentity,
		SignCheckIssuer:   testIssuer,
		MaxSignatureOps:   10,
	}
}

// newSignCheckRegistry starts a test registry and points sigcheck to its
// production repository.
func newSignCheckRegistry(t *testing.T) (string, *DefaultPromoterImplementation) {
	t.Helper()

	host, di := newTLSTestRegistry(t)
	di.signCheckRepo = host + "/" + productionRepositoryPath

	return di.signCheckRepo, di
}

// testCertificate returns a certificate for the public key with the
// identity and OIDC issuer extension Fulcio sets. It is not signed by a
// trusted CA, sigcheck only reads the identity.
func testCertificate(t *testing.T, pub crypto.PublicKey, identity string) *x509.Certificate {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	issuer, err := asn1.MarshalWithParams(testIssuer, "utf8")
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(1),
		Subject:         pkix.Name{CommonName: "sigstore"},
		NotBefore:       time.Now().Add(-time.Hour),
		NotAfter:        time.Now().Add(time.Hour),
		EmailAddresses:  []string{identity},
		ExtKeyUsage:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		ExtraExtensions: []pkix.Extension{{Id: certificate.OIDIssuerV2, Value: issuer}},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, caKey)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	return cert
}

// pushTestSignature pushes a cosign signature tag for the digest, with one
// signature layer per identity signing the given docker reference.
func pushTestSignature(
	t *testing.T, di *DefaultPromoterImplementation, repo, digest, dockerRef string, identities ...string,
) {
	t.Helper()

	signed, err := json.Marshal(payload.SimpleContainerImage{
		Critical: payload.Critical{
			Identity: payload.Identity{DockerReference: dockerRef},
			Image:    payload.Image{DockerManifestDigest: digest},
			Type:     payload.CosignSignatureType,
		},
	})
	require.NoError(t, err)

	sigImage := empty.Image

	for _, identity := range identities {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		certPEM, err := cryptoutils.MarshalCertificateToPEM(testCertificate(t, key.Public(), identity))
		require.NoError(t, err)

		sigImage, err = mutate.Append(sigImage, mutate.Addendum{
			Layer: static.NewLayer(signed, simpleSigningMediaType),
			Annotations: map[string]string{
				certificateAnnotation:                string(certPEM),
				"dev.cosignproject.cosign/signature": identity,
			},
		})
		require.NoError(t, err)
	}

	ref, err := name.ParseReference(repo + ":" + digestToSignatureTag(image.Digest(digest)))
	require.NoError(t, err)
	require.NoError(t, remote.Write(ref, sigImage, remote.WithTransport(di.getTransport())))
}

// pushTestCosignManifest pushes a manifest like cosign pushes for the .sig
// and .att tags: a plain image config and one layer of the given media
// type. It returns the manifest digest.
func pushTestCosignManifest(t *testing.T, di *DefaultPromoterImplementation, ref, layerMediaType string) string {
	t.Helper()

	img, err := mutate.Append(empty.Image, mutate.Addendum{
		Layer: static.NewLayer([]byte(`{}`), types.MediaType(layerMediaType)),
	})
	require.NoError(t, err)

	img = mutate.ConfigMediaType(mutate.MediaType(img, types.OCIManifestSchema1), types.OCIConfigJSON)

	r, err := name.ParseReference(ref)
	require.NoError(t, err)
	require.NoError(t, remote.Write(r, img, remote.WithTransport(di.getTransport())))

	digest, err := img.Digest()
	require.NoError(t, err)

	return digest.String()
}

// certBundleSigner signs statements into sigstore bundles carrying a test
// certificate for the given identity, like the keyless production signer.
type certBundleSigner struct {
	identity string
	t        *testing.T
}

func (c *certBundleSigner) SignStatement(statement []byte) ([]byte, error) {
	keypair, err := sgsign.NewEphemeralKeypair(nil)
	if err != nil {
		return nil, fmt.Errorf("creating keypair: %w", err)
	}

	bndl, err := sgsign.Bundle(
		&sgsign.DSSEData{Data: statement, PayloadType: "application/vnd.in-toto+json"},
		keypair,
		sgsign.BundleOptions{CertificateProvider: &testCertificateProvider{
			cert: testCertificate(c.t, keypair.GetPublicKey(), c.identity),
		}},
	)
	if err != nil {
		return nil, fmt.Errorf("signing test bundle: %w", err)
	}

	data, err := protojson.Marshal(bndl)
	if err != nil {
		return nil, fmt.Errorf("serializing test bundle: %w", err)
	}

	return data, nil
}

// testCertificateProvider hands out a fixed certificate instead of Fulcio.
type testCertificateProvider struct {
	cert *x509.Certificate
}

func (p *testCertificateProvider) GetCertificate(
	context.Context, sgsign.Keypair, *sgsign.CertificateProviderOptions,
) ([]byte, error) {
	return p.cert.Raw, nil
}

// pushTestAttestation attaches a promotion attestation to the digest,
// signed by identity, with subject as the statement subject name.
func pushTestAttestation(
	t *testing.T, di *DefaultPromoterImplementation, repo, digest, subject, identity string,
) {
	t.Helper()

	digestRef, err := name.NewDigest(repo + "@" + digest)
	require.NoError(t, err)

	signer := di.attSigner
	di.attSigner = &certBundleSigner{identity: identity, t: t}

	defer func() { di.attSigner = signer }()

	require.NoError(t, di.writeAttestation(
		context.Background(), digestRef, &provenance.PromotionGenerator{},
		&provenance.PromotionRecord{DstRef: subject, Digest: digest},
	))
}

func TestProductionImageName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		ref     string
		want    string
		wantErr bool
	}{
		{ref: "registry.k8s.io/kube-apiserver:v1.34.0", want: "kube-apiserver"},
		{ref: "registry.k8s.io/sig-storage/nfs-provisioner@" + testDigest, want: "sig-storage/nfs-provisioner"},
		{ref: "us-central1-docker.pkg.dev/k8s-artifacts-prod/images/pause:3.10", want: "pause"},
		{ref: "europe-west1-docker.pkg.dev/k8s-artifacts-prod/images/sig-storage/nfs:v1", want: "sig-storage/nfs"},
		{ref: "us-central1-docker.pkg.dev/k8s-staging-foo/images/pause:3.10", wantErr: true},
		{ref: "gcr.io/k8s-staging-foo/pause:3.10", wantErr: true},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			t.Parallel()

			ref, err := name.ParseReference(tc.ref)
			require.NoError(t, err)

			got, err := productionImageName(ref)
			if tc.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestImageForIdentifier(t *testing.T) {
	t.Parallel()

	tags := &google.Tags{Manifests: map[string]google.ManifestInfo{
		testDigest:        {Tags: []string{testTagV1, testTagStable}},
		"sha256:tagless1": {},
	}}

	img, ok := imageForIdentifier(testImageApp, tags, testTagStable)
	require.True(t, ok)
	require.Equal(t, checkresults.Image{
		Name: testImageApp, Digest: testDigest, Tags: []string{testTagStable, testTagV1},
	}, img)

	img, ok = imageForIdentifier(testImageApp, tags, testDigest)
	require.True(t, ok)
	require.Equal(t, testDigest, img.Digest)

	img, ok = imageForIdentifier(testImageApp, tags, "sha256:tagless1")
	require.True(t, ok)
	require.Empty(t, img.Tags)

	_, ok = imageForIdentifier(testImageApp, tags, "v2.0")
	require.False(t, ok)
}

func TestSignCheckIdentity(t *testing.T) {
	t.Parallel()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	signer := testCertificate(t, key.Public(), testSignerIdentity)
	other := testCertificate(t, key.Public(), testOtherIdentity)

	for _, tc := range []struct {
		name        string
		modify      func(*options.Options)
		signerMatch bool
		otherMatch  bool
	}{
		{
			name:        "exact identity",
			modify:      func(*options.Options) {},
			signerMatch: true,
		},
		{
			name: "identity regexp takes precedence",
			modify: func(o *options.Options) {
				o.SignCheckIdentityRegexp = testOtherIdentityRegexp
			},
			otherMatch: true,
		},
		{
			name: "identity regexp matching both",
			modify: func(o *options.Options) {
				o.SignCheckIdentityRegexp = `(someone@example\.com)|(krel-trust@k8s-releng-prod\.iam\.gserviceaccount\.com)`
			},
			signerMatch: true,
			otherMatch:  true,
		},
		{
			name: "other issuer",
			modify: func(o *options.Options) {
				o.SignCheckIssuer = "https://token.actions.githubusercontent.com"
			},
		},
		{
			name: "issuer regexp takes precedence",
			modify: func(o *options.Options) {
				o.SignCheckIssuer = "https://token.actions.githubusercontent.com"
				o.SignCheckIssuerRegexp = `^https://accounts\.google\.com$`
			},
			signerMatch: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := testSignCheckOptions()
			tc.modify(opts)

			identity, err := signCheckIdentity(opts)
			require.NoError(t, err)
			require.Equal(t, tc.signerMatch, matchesIdentity(identity, signer))
			require.Equal(t, tc.otherMatch, matchesIdentity(identity, other))
		})
	}

	_, err = signCheckIdentity(&options.Options{SignCheckIssuer: testIssuer})
	require.Error(t, err, "an identity is required")
}

func TestCheckSignerAccount(t *testing.T) {
	t.Parallel()

	opts := testSignCheckOptions()
	require.NoError(t, checkSignerAccount(opts))

	opts.SignCheckIdentityRegexp = `@k8s-releng-prod\.iam\.gserviceaccount\.com$`
	require.NoError(t, checkSignerAccount(opts))

	// The regexp takes precedence, so the matching exact identity does
	// not help.
	opts.SignCheckIdentityRegexp = testOtherIdentityRegexp
	require.Error(t, checkSignerAccount(opts))

	opts = testSignCheckOptions()
	opts.SignerAccount = testOtherIdentity
	require.Error(t, checkSignerAccount(opts))

	// Neither signing nor attesting starts with a mismatching account.
	di := &DefaultPromoterImplementation{}
	img := checkresults.Image{Name: testImageApp, Digest: testDigest, Tags: []string{testTagV1}, Attest: true}
	results := checkresults.Results{{Image: img}}

	require.Error(t, di.FixMissingSignatures(context.Background(), opts, results))
	require.Error(t, di.FixMissingAttestations(context.Background(), opts, results, nil))
}

func TestIsMetadataTag(t *testing.T) {
	t.Parallel()

	require.False(t, isMetadataTag(nil))
	require.False(t, isMetadataTag([]string{testTagV1, testTagStable}))

	for _, tag := range []string{"sha256-def.sig", "sha256-def.att", "sha256-def.sbom"} {
		require.True(t, isMetadataTag([]string{tag}))
		require.True(t, isMetadataTag([]string{testTagStable, tag}), "not only the first tag")
	}
}

func TestIsSigstoreArtifact(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		manifest string
		want     bool
	}{
		{
			name: "image",
			manifest: `{"config": {"mediaType": "application/vnd.oci.image.config.v1+json"},
				"layers": [{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip"}]}`,
		},
		{
			name: "cosign .sig manifest",
			manifest: `{"config": {"mediaType": "application/vnd.oci.image.config.v1+json"},
				"layers": [{"mediaType": "application/vnd.dev.cosign.simplesigning.v1+json"}]}`,
			want: true,
		},
		{
			name: "cosign .att manifest",
			manifest: `{"config": {"mediaType": "application/vnd.oci.image.config.v1+json"},
				"layers": [{"mediaType": "application/vnd.dsse.envelope.v1+json"}]}`,
			want: true,
		},
		{
			name: "in-toto layer",
			manifest: `{"config": {"mediaType": "application/vnd.oci.image.config.v1+json"},
				"layers": [{"mediaType": "application/vnd.in-toto+json"}]}`,
			want: true,
		},
		{
			name:     "other artifact",
			manifest: `{"artifactType": "application/spdx+json", "config": {"mediaType": "application/vnd.oci.empty.v1+json"}}`,
		},
		{
			name:     "attestation bundle",
			manifest: `{"artifactType": "application/vnd.dev.sigstore.bundle.v0.3+json"}`,
			want:     true,
		},
		{
			name:     "cosign signature",
			manifest: `{"config": {"mediaType": "application/vnd.dev.cosign.artifact.sig.v1+json"}}`,
			want:     true,
		},
		{
			name:     "invalid",
			manifest: `{`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, isSigstoreArtifact([]byte(tc.manifest)))
		})
	}
}

func TestSelectImages(t *testing.T) {
	t.Parallel()

	repo, di := newSignCheckRegistry(t)
	appRepo := repo + "/app"

	// A tagged index, whose children have no tags.
	index, err := random.Index(1024, 1, 2)
	require.NoError(t, err)

	indexRef, err := name.ParseReference(appRepo + ":v1.0")
	require.NoError(t, err)
	require.NoError(t, remote.WriteIndex(indexRef, index, remote.WithTransport(di.getTransport())))

	indexDigest, err := index.Digest()
	require.NoError(t, err)

	indexManifest, err := index.IndexManifest()
	require.NoError(t, err)

	// A digest promoted without a tag, with a promotion attestation.
	tagless := pushTestImage(t, di, appRepo+":tagless")
	pushTestAttestation(t, di, appRepo, tagless, "registry.k8s.io/app", testSignerIdentity)

	taglessRef, err := name.NewDigest(appRepo + "@" + tagless)
	require.NoError(t, err)

	referrers, err := ociremote.Referrers(
		taglessRef, "", ociremote.WithRemoteOptions(di.remoteOptions()...),
	)
	require.NoError(t, err)
	require.Len(t, referrers.Manifests, 1)

	// Cosign signature and attestation manifests left without a tag, for
	// example when promotion signs on top of the copied staging signature
	// and the signature tag moves to the new manifest.
	oldSignature := pushTestCosignManifest(t, di, appRepo+":old-sig", simpleSigningMediaType)
	oldAttestation := pushTestCosignManifest(t, di, appRepo+":old-att", "application/vnd.dsse.envelope.v1+json")

	now := time.Now()
	from := now.Add(-time.Hour)
	recent := now.Add(-time.Minute)

	manifests := map[string]google.ManifestInfo{
		indexDigest.String(): {
			MediaType: string(types.OCIImageIndex), Uploaded: recent, Tags: []string{testTagV1, testTagStable},
		},
		tagless: {MediaType: string(types.DockerManifestSchema2), Uploaded: recent},
		referrers.Manifests[0].Digest.String(): {
			MediaType: string(types.OCIManifestSchema1), Uploaded: recent,
		},
		oldSignature:   {MediaType: string(types.OCIManifestSchema1), Uploaded: recent},
		oldAttestation: {MediaType: string(types.OCIManifestSchema1), Uploaded: recent},
		"sha256:sig": {
			MediaType: string(types.DockerManifestSchema2), Uploaded: recent, Tags: []string{"sha256-def.sig"},
		},
		"sha256:att": {
			MediaType: string(types.DockerManifestSchema2), Uploaded: recent, Tags: []string{"sha256-def.att"},
		},
		"sha256:old": {
			MediaType: string(types.DockerManifestSchema2), Uploaded: now.Add(-2 * time.Hour), Tags: []string{"v0.9"},
		},
		"sha256:future": {
			MediaType: string(types.DockerManifestSchema2), Uploaded: now.Add(time.Hour), Tags: []string{"v1.1"},
		},
	}

	for i := range indexManifest.Manifests {
		manifests[indexManifest.Manifests[i].Digest.String()] = google.ManifestInfo{
			MediaType: string(indexManifest.Manifests[i].MediaType),
			// Children are uploaded before their index.
			Uploaded: recent.Add(-time.Second),
		}
	}

	indexImage := checkresults.Image{
		Name: testImageApp, Digest: indexDigest.String(), Tags: []string{testTagStable, testTagV1}, Uploaded: recent,
	}

	images, err := di.selectImages(
		context.Background(), testImageApp, &google.Tags{Manifests: manifests}, from, now, time.Time{},
	)
	require.NoError(t, err)
	require.ElementsMatch(t, []checkresults.Image{
		indexImage,
		{Name: testImageApp, Digest: tagless, Uploaded: recent},
	}, images)

	// Digests without a tag promoted before attestations are not checked
	// at all, images with tags still are for their signature.
	images, err = di.selectImages(
		context.Background(), testImageApp, &google.Tags{Manifests: manifests}, from, now, now,
	)
	require.NoError(t, err)
	require.Equal(t, []checkresults.Image{indexImage}, images)
}

func TestAttestationsSince(t *testing.T) {
	t.Parallel()

	since, err := attestationsSince(&options.Options{SignCheckAttestationsSince: "2026-09-24"})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, time.September, 24, 0, 0, 0, 0, time.UTC), since)

	since, err = attestationsSince(&options.Options{})
	require.NoError(t, err)
	require.True(t, since.IsZero())

	_, err = attestationsSince(&options.Options{SignCheckAttestationsSince: "24.09.2026"})
	require.Error(t, err)
}

func TestApplyAttestationsSince(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, time.September, 24, 0, 0, 0, 0, time.UTC)
	before := since.Add(-time.Second)

	taggedBefore := checkresults.Image{Name: "before", Digest: "sha256:1", Tags: []string{testTagV1}, Uploaded: before}
	taglessBefore := checkresults.Image{Name: "before", Digest: "sha256:2", Uploaded: before}
	taggedSince := checkresults.Image{Name: "since", Digest: "sha256:3", Tags: []string{testTagV1}, Uploaded: since}
	taglessSince := checkresults.Image{Name: "since", Digest: "sha256:4", Uploaded: since}

	images := []checkresults.Image{taggedBefore, taglessBefore, taggedSince, taglessSince}

	attest := func(img checkresults.Image) checkresults.Image {
		img.Attest = true

		return img
	}

	// Before the cutoff, images are only checked for their signature, so
	// the ones without a tag are dropped.
	require.Equal(t, []checkresults.Image{
		taggedBefore, attest(taggedSince), attest(taglessSince),
	}, applyAttestationsSince(images, since))

	// Without a cutoff, all images are checked for attestations.
	require.Equal(t, []checkresults.Image{
		attest(taggedBefore), attest(taglessBefore), attest(taggedSince), attest(taglessSince),
	}, applyAttestationsSince(images, time.Time{}))
}

// signCheckFixture pushes images in all states sigcheck distinguishes.
type signCheckFixture struct {
	good, otherIdentity, wrongReference, tagless, missing checkresults.Image
}

func newSignCheckFixture(t *testing.T, di *DefaultPromoterImplementation, repo string) *signCheckFixture {
	t.Helper()

	push := func(imgName string, tags ...string) checkresults.Image {
		tag := "tagless"
		if len(tags) > 0 {
			tag = tags[0]
		}

		digest := pushTestImage(t, di, repo+"/"+imgName+":"+tag)

		return checkresults.Image{Name: imgName, Digest: digest, Tags: tags, Attest: true}
	}

	f := &signCheckFixture{
		good:           push("good", testTagV1),
		otherIdentity:  push("other-identity", testTagV1),
		wrongReference: push("wrong-reference", testTagV1),
		tagless:        push("tagless"),
		missing:        push("missing", testTagV1),
	}

	// Signed by the promoter and, like promoted images, by the staging
	// identity whose signature is copied from staging.
	pushTestSignature(t, di, repo+"/good", f.good.Digest, "registry.k8s.io/good",
		testOtherIdentity, testSignerIdentity)
	pushTestAttestation(t, di, repo+"/good", f.good.Digest, "registry.k8s.io/good", testSignerIdentity)

	pushTestSignature(t, di, repo+"/other-identity", f.otherIdentity.Digest,
		"registry.k8s.io/other-identity", testOtherIdentity)
	pushTestAttestation(t, di, repo+"/other-identity", f.otherIdentity.Digest,
		"registry.k8s.io/other-identity", testOtherIdentity)

	// Signed and attested for the regional instead of the production
	// reference.
	pushTestSignature(t, di, repo+"/wrong-reference", f.wrongReference.Digest,
		repo+"/wrong-reference", testSignerIdentity)
	pushTestAttestation(t, di, repo+"/wrong-reference", f.wrongReference.Digest,
		repo+"/wrong-reference", testSignerIdentity)

	pushTestAttestation(t, di, repo+"/tagless", f.tagless.Digest, "registry.k8s.io/tagless", testSignerIdentity)

	return f
}

func TestGetSignatureStatus(t *testing.T) {
	t.Parallel()

	repo, di := newSignCheckRegistry(t)
	f := newSignCheckFixture(t, di, repo)

	images := []checkresults.Image{f.good, f.otherIdentity, f.wrongReference, f.tagless, f.missing}

	results, err := di.GetSignatureStatus(context.Background(), testSignCheckOptions(), images)
	require.NoError(t, err)
	require.Equal(t, checkresults.Results{
		{Image: f.good, Signed: true, Attested: true},
		{Image: f.otherIdentity},
		{Image: f.wrongReference},
		// Images without tags are not signed by promotion.
		{Image: f.tagless, Attested: true},
		{Image: f.missing},
	}, results)

	require.Equal(t, checkresults.Results{results[1], results[2], results[4]}, results.Unsigned())
	require.Equal(t, checkresults.Results{results[1], results[2], results[4]}, results.Unattested())

	// The identity regexp takes precedence over the exact identity.
	opts := testSignCheckOptions()
	opts.SignCheckIdentityRegexp = testOtherIdentityRegexp

	results, err = di.GetSignatureStatus(context.Background(), opts, []checkresults.Image{f.good, f.otherIdentity})
	require.NoError(t, err)
	require.Equal(t, checkresults.Results{
		// Only the staging signature matches.
		{Image: f.good, Signed: true},
		{Image: f.otherIdentity, Signed: true, Attested: true},
	}, results)
}

// recordingPromotionGenerator records the promotion records and generates
// real statements from them.
type recordingPromotionGenerator struct {
	provenance.PromotionGenerator

	mu      sync.Mutex
	records []*provenance.PromotionRecord
}

func (r *recordingPromotionGenerator) Generate(
	ctx context.Context, record *provenance.PromotionRecord,
) ([]byte, error) {
	r.mu.Lock()
	r.records = append(r.records, record)
	r.mu.Unlock()

	data, err := r.PromotionGenerator.Generate(ctx, record)
	if err != nil {
		return nil, fmt.Errorf("generating statement: %w", err)
	}

	return data, nil
}

func TestFixMissingAttestations(t *testing.T) {
	t.Parallel()

	repo, di := newSignCheckRegistry(t)
	f := newSignCheckFixture(t, di, repo)
	opts := testSignCheckOptions()

	results, err := di.GetSignatureStatus(
		context.Background(), opts, []checkresults.Image{f.good, f.otherIdentity, f.missing},
	)
	require.NoError(t, err)

	di.attSigner = &certBundleSigner{identity: testSignerIdentity, t: t}
	gen := &recordingPromotionGenerator{}

	require.NoError(t, di.FixMissingAttestations(context.Background(), opts, results, gen))

	// Only unattested images are attested, including the one with an
	// attestation of another identity.
	require.Len(t, gen.records, 2)

	records := map[string]*provenance.PromotionRecord{}
	for _, record := range gen.records {
		records[record.GetDstRef()] = record
	}

	record := records["registry.k8s.io/missing"]
	require.NotNil(t, record)
	require.Equal(t, f.missing.Digest, record.GetDigest())
	require.Equal(t, []string{testTagV1}, record.GetTags())
	require.Equal(t, "registry.k8s.io/missing", record.GetDestination().GetName())
	require.NotEmpty(t, record.GetBuilderId())
	require.NotNil(t, record.GetTimestamp())

	// The staging image and manifest are not known to sigcheck.
	require.Empty(t, record.GetSrcRef())
	require.Nil(t, record.GetSource())
	require.Nil(t, record.GetManifest())

	require.Contains(t, records, "registry.k8s.io/other-identity")

	results, err = di.GetSignatureStatus(
		context.Background(), opts, []checkresults.Image{f.otherIdentity, f.missing},
	)
	require.NoError(t, err)
	require.Empty(t, results.Unattested())
}

func TestFixMissingAttestationsTagless(t *testing.T) {
	t.Parallel()

	repo, di := newSignCheckRegistry(t)
	digest := pushTestImage(t, di, repo+"/app:tagless")
	img := checkresults.Image{Name: testImageApp, Digest: digest, Attest: true}

	di.attSigner = &certBundleSigner{identity: testSignerIdentity, t: t}
	gen := &recordingPromotionGenerator{}

	require.NoError(t, di.FixMissingAttestations(
		context.Background(), testSignCheckOptions(), checkresults.Results{{Image: img}}, gen,
	))

	require.Len(t, gen.records, 1)
	require.Equal(t, "registry.k8s.io/app", gen.records[0].GetDstRef())
	require.Empty(t, gen.records[0].GetTags())

	results, err := di.GetSignatureStatus(context.Background(), testSignCheckOptions(), []checkresults.Image{img})
	require.NoError(t, err)
	require.Equal(t, checkresults.Results{{Image: img, Attested: true}}, results)
}

func TestFixMissingNothingToDo(t *testing.T) {
	t.Parallel()

	// No identity token provider and no signers: nothing may be signed.
	di := &DefaultPromoterImplementation{}
	opts := &options.Options{}

	tagged := checkresults.Image{Name: testImageApp, Digest: testDigest, Tags: []string{testTagV1}}
	tagless := checkresults.Image{Name: testImageApp, Digest: testDigest}
	results := checkresults.Results{
		{Image: tagged, Signed: true, Attested: true},
		{Image: tagless, Attested: true},
	}

	require.NoError(t, di.FixMissingSignatures(context.Background(), opts, results))
	require.NoError(t, di.FixMissingAttestations(context.Background(), opts, results, nil))
}

func TestSignCheckEdges(t *testing.T) {
	t.Parallel()

	di := &DefaultPromoterImplementation{}

	tagged := di.signCheckEdges(&checkresults.Image{
		Name: "sig-storage/csi-provisioner", Digest: testDigest, Tags: []string{testTagV1, "v1"},
	})
	require.Len(t, tagged, 2)

	for i := range tagged {
		require.Equal(t, "registry.k8s.io/sig-storage/csi-provisioner", targetIdentity(&tagged[i]))
		require.Equal(t,
			"us-central1-docker.pkg.dev/k8s-artifacts-prod/images/sig-storage/csi-provisioner@"+testDigest,
			canonicalDigestRef(&tagged[i]),
		)
	}

	require.Equal(t, image.Tag(testTagV1), tagged[0].DstImageTag.Tag)
	require.Equal(t, image.Tag("v1"), tagged[1].DstImageTag.Tag)

	tagless := di.signCheckEdges(&checkresults.Image{Name: "pause", Digest: testDigest})
	require.Len(t, tagless, 1)
	require.Empty(t, tagless[0].DstImageTag.Tag)
}

func TestAttestationNotRequired(t *testing.T) {
	t.Parallel()

	repo, di := newSignCheckRegistry(t)
	f := newSignCheckFixture(t, di, repo)

	// Promoted before attestations: the missing attestation is neither a
	// problem nor repaired.
	old := f.missing
	old.Attest = false

	results, err := di.GetSignatureStatus(context.Background(), testSignCheckOptions(), []checkresults.Image{old})
	require.NoError(t, err)
	require.Equal(t, checkresults.Results{{Image: old}}, results)
	require.Empty(t, results.Unattested())
	require.Equal(t, results, results.Unsigned())

	// No attestation signer is set, so writing one would fail.
	gen := &recordingPromotionGenerator{}
	require.NoError(t, di.FixMissingAttestations(context.Background(), testSignCheckOptions(), results, gen))
	require.Empty(t, gen.records)
}
