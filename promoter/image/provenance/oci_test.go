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
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/policylabs/attestation"
	sapi "github.com/policylabs/signer/api/v1"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

const (
	testPredicateType = "https://slsa.dev/provenance/v1"
	testBundleSigner  = "sigstore::https://accounts.google.com::builder@example.iam.gserviceaccount.com"
)

// newReferrersRepo starts an in-process registry with referrers support
// and returns a repository.
func newReferrersRepo(t *testing.T) name.Repository {
	t.Helper()

	s := httptest.NewServer(registry.New(registry.WithReferrersSupport(true)))
	t.Cleanup(s.Close)

	repo, err := name.NewRepository(s.Listener.Addr().String() + "/test/image")
	require.NoError(t, err)

	return repo
}

// pushImage pushes a random image and returns its digest reference.
func pushImage(t *testing.T, repo name.Repository) name.Digest {
	t.Helper()

	img, err := random.Image(256, 1)
	require.NoError(t, err)

	dg, err := img.Digest()
	require.NoError(t, err)

	ref := repo.Digest(dg.String())
	require.NoError(t, remote.Write(ref, img))

	return ref
}

// inTotoStatementType is the type of in-toto v1 statements.
const inTotoStatementType = "https://in-toto.io/Statement/v1"

// inTotoStatement is an in-toto statement.
type inTotoStatement struct {
	Type          string          `json:"_type"` //nolint:tagliatelle // in-toto field name
	Subject       []inTotoSubject `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     any             `json:"predicate"`
}

// inTotoSubject is a subject of an in-toto statement.
type inTotoSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// statement returns an in-toto statement about the digest.
func statement(t *testing.T, digest name.Digest, predicateType string) []byte {
	t.Helper()

	hex := strings.TrimPrefix(digest.DigestStr(), "sha256:")

	data, err := json.Marshal(inTotoStatement{
		Type:          inTotoStatementType,
		Subject:       []inTotoSubject{{Name: "image", Digest: map[string]string{"sha256": hex}}},
		PredicateType: predicateType,
		Predicate:     map[string]any{},
	})
	require.NoError(t, err)

	return data
}

// dsseEnvelope returns a DSSE envelope holding the statement.
func dsseEnvelope(t *testing.T, payload []byte, signed bool) map[string]any {
	t.Helper()

	env := map[string]any{
		"payloadType": "application/vnd.in-toto+json",
		"payload":     base64.StdEncoding.EncodeToString(payload),
		"signatures":  []any{},
	}

	if signed {
		env["signatures"] = []any{map[string]string{"sig": base64.StdEncoding.EncodeToString([]byte("sig"))}}
	}

	return env
}

// sigstoreBundle returns a sigstore bundle holding the statement. It is
// not signed properly, the tests replace the verification.
func sigstoreBundle(t *testing.T, payload []byte) []byte {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"mediaType":            "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{"publicKey": map[string]string{"hint": "test"}},
		"dsseEnvelope":         dsseEnvelope(t, payload, true),
	})
	require.NoError(t, err)

	return data
}

// rawManifest is a manifest pushed as is.
type rawManifest struct {
	data      []byte
	mediaType types.MediaType
}

func (m rawManifest) RawManifest() ([]byte, error) { return m.data, nil }

func (m rawManifest) MediaType() (types.MediaType, error) { return m.mediaType, nil }

// pushReferrer attaches an artifact with a single layer to the subject and
// returns the digest of the referrer manifest.
func pushReferrer(
	t *testing.T, subject name.Digest, artifactType string, layerType types.MediaType, content []byte,
) v1.Hash {
	t.Helper()

	layer := static.NewLayer(content, layerType)
	require.NoError(t, remote.WriteLayer(subject.Context(), layer))

	config := static.NewLayer([]byte("{}"), "application/vnd.oci.empty.v1+json")
	require.NoError(t, remote.WriteLayer(subject.Context(), config))

	subjectDesc, err := remote.Head(subject)
	require.NoError(t, err)

	mf := v1.Manifest{
		SchemaVersion: 2,
		MediaType:     types.OCIManifestSchema1,
		ArtifactType:  artifactType,
		Config:        descriptor(t, config),
		Layers:        []v1.Descriptor{descriptor(t, layer)},
		Subject:       subjectDesc,
	}

	data, err := json.Marshal(mf)
	require.NoError(t, err)

	dg, _, err := v1.SHA256(strings.NewReader(string(data)))
	require.NoError(t, err)

	require.NoError(t, remote.Put(
		subject.Context().Digest(dg.String()),
		rawManifest{data: data, mediaType: types.OCIManifestSchema1},
	))

	return dg
}

// pushIndexReferrer attaches an empty index to the subject and returns its
// digest.
func pushIndexReferrer(t *testing.T, subject name.Digest, artifactType string) v1.Hash {
	t.Helper()

	subjectDesc, err := remote.Head(subject)
	require.NoError(t, err)

	data, err := json.Marshal(v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     types.OCIImageIndex,
		ArtifactType:  artifactType,
		Manifests:     []v1.Descriptor{},
		Subject:       subjectDesc,
	})
	require.NoError(t, err)

	dg, _, err := v1.SHA256(strings.NewReader(string(data)))
	require.NoError(t, err)

	require.NoError(t, remote.Put(
		subject.Context().Digest(dg.String()),
		rawManifest{data: data, mediaType: types.OCIImageIndex},
	))

	return dg
}

// descriptor describes a layer.
func descriptor(t *testing.T, l v1.Layer) v1.Descriptor {
	t.Helper()

	dg, err := l.Digest()
	require.NoError(t, err)

	size, err := l.Size()
	require.NoError(t, err)

	mt, err := l.MediaType()
	require.NoError(t, err)

	return v1.Descriptor{MediaType: mt, Digest: dg, Size: size}
}

// pushAttestationTag pushes a legacy cosign `.att` tag with a single layer.
func pushAttestationTag(t *testing.T, subject name.Digest, mediaType types.MediaType, data []byte) {
	t.Helper()

	layer := static.NewLayer(data, mediaType)
	require.NoError(t, remote.WriteLayer(subject.Context(), layer))

	config := static.NewLayer([]byte("{}"), types.OCIConfigJSON)
	require.NoError(t, remote.WriteLayer(subject.Context(), config))

	mf, err := json.Marshal(v1.Manifest{
		SchemaVersion: 2,
		MediaType:     types.OCIManifestSchema1,
		Config:        descriptor(t, config),
		Layers:        []v1.Descriptor{descriptor(t, layer)},
	})
	require.NoError(t, err)

	tag := subject.Context().Tag(digestToAttestationTag(image.Digest(subject.DigestStr())))
	require.NoError(t, remote.Put(tag, rawManifest{data: mf, mediaType: types.OCIManifestSchema1}))
}

// verifiedAs returns a verify function that records a verified signature
// by the given Google account.
func verifiedAs(identity string) func(attestation.Envelope) error {
	return func(env attestation.Envelope) error {
		env.GetPredicate().SetVerification(&sapi.Verification{
			Signature: &sapi.SignatureVerification{
				Status:   sapi.VerificationStatus_VERIFIED,
				Verified: true,
				Identities: []*sapi.Identity{{
					Sigstore: &sapi.IdentitySigstore{Issuer: testIssuer, Identity: identity},
				}},
			},
		})

		return nil
	}
}

func TestDiscoverNoAttestations(t *testing.T) {
	ref := pushImage(t, newReferrersRepo(t))

	d := &OCIDiscoverer{}
	discovery, err := d.Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Equal(t, ref.String(), discovery.Reference)
	require.Empty(t, discovery.Attestations)
	require.Empty(t, discovery.Referrers)
	require.Empty(t, discovery.Children)
	require.Empty(t, discovery.Summary())
}

func TestDiscoverBundleReferrer(t *testing.T) {
	ref := pushImage(t, newReferrersRepo(t))
	bundle := sigstoreBundle(t, statement(t, ref, testPredicateType))
	loc := pushReferrer(t, ref, BundleArtifactType, BundleArtifactType, bundle)

	layer, _, err := v1.SHA256(bytes.NewReader(bundle))
	require.NoError(t, err)

	d := &OCIDiscoverer{
		verifyFn: verifiedAs("builder@example.iam.gserviceaccount.com"),
	}
	discovery, err := d.Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Empty(t, discovery.Referrers)
	require.Len(t, discovery.Attestations, 1)

	att := discovery.Attestations[0]
	require.Equal(t, ref.DigestStr(), att.Digest)
	require.Equal(t, SourceReferrer, att.Source)
	require.Equal(t, loc.String(), att.Location)
	require.Equal(t, layer.String(), att.Layer)
	require.Equal(t, testPredicateType, att.PredicateType)
	require.Equal(t, SignatureVerified, att.Status)
	require.Equal(t, []string{testBundleSigner}, att.Signers)
	require.NotNil(t, att.Envelope)

	require.Equal(t, []string{fmt.Sprintf(
		"%s referrer %s: %s (verified by %s)", ref.DigestStr(), loc, testPredicateType, testBundleSigner,
	)}, discovery.Summary())
}

func TestDiscoverBundleLayerWithoutArtifactType(t *testing.T) {
	// Registries don't always keep the artifact type, the layer media type
	// is enough.
	ref := pushImage(t, newReferrersRepo(t))
	pushReferrer(t, ref, "", BundleArtifactType, sigstoreBundle(t, statement(t, ref, testPredicateType)))

	d := &OCIDiscoverer{verifyFn: verifiedAs("a@example.com")}
	discovery, err := d.Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Len(t, discovery.Attestations, 1)
	require.Equal(t, testPredicateType, discovery.Attestations[0].PredicateType)
}

func TestDiscoverOtherReferrer(t *testing.T) {
	ref := pushImage(t, newReferrersRepo(t))
	loc := pushReferrer(t, ref, "application/spdx+json", "application/spdx+json", []byte("{}"))

	d := &OCIDiscoverer{verifyFn: func(attestation.Envelope) error { return nil }}
	discovery, err := d.Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Empty(t, discovery.Attestations)
	require.Equal(t, []Referrer{{
		Digest:       ref.DigestStr(),
		Location:     loc.String(),
		ArtifactType: "application/spdx+json",
	}}, discovery.Referrers)
	require.Equal(t, []string{fmt.Sprintf(
		"%s referrer %s: application/spdx+json (no attestation)", ref.DigestStr(), loc,
	)}, discovery.Summary())
}

func TestDiscoverIndexChildren(t *testing.T) {
	repo := newReferrersRepo(t)

	platforms, err := random.Index(256, 1, 2)
	require.NoError(t, err)

	attestationManifest, err := random.Image(256, 1)
	require.NoError(t, err)

	annotated, err := random.Image(256, 1)
	require.NoError(t, err)

	annotations := map[string]string{dockerReferenceTypeAnnotation: dockerAttestationManifest}

	// BuildKit adds attestation manifests to an index, which are no
	// platform manifests. The annotation alone doesn't hide an image with
	// a real platform.
	idx := mutate.AppendManifests(platforms,
		mutate.IndexAddendum{
			Add:         attestationManifest,
			Annotations: annotations,
			Platform:    &v1.Platform{OS: unknownPlatform, Architecture: unknownPlatform},
		},
		mutate.IndexAddendum{
			Add:         annotated,
			Annotations: annotations,
			Platform:    &v1.Platform{OS: "linux", Architecture: "arm64"},
		},
	)

	idxDigest, err := idx.Digest()
	require.NoError(t, err)

	ref := repo.Digest(idxDigest.String())
	require.NoError(t, remote.WriteIndex(ref, idx))

	im, err := idx.IndexManifest()
	require.NoError(t, err)

	child := repo.Digest(im.Manifests[1].Digest.String())
	pushReferrer(t, child, BundleArtifactType, BundleArtifactType,
		sigstoreBundle(t, statement(t, child, testPredicateType)))

	d := &OCIDiscoverer{verifyFn: verifiedAs("a@example.com")}
	discovery, err := d.Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Len(t, discovery.Attestations, 1)
	require.Equal(t, child.DigestStr(), discovery.Attestations[0].Digest)
	require.Equal(t, []string{
		im.Manifests[0].Digest.String(), child.DigestStr(), im.Manifests[3].Digest.String(),
	}, discovery.Children)
}

func TestDiscoverAttestationTag(t *testing.T) {
	ref := pushImage(t, newReferrersRepo(t))
	env, err := json.Marshal(dsseEnvelope(t, statement(t, ref, testPredicateType), false))
	require.NoError(t, err)
	pushAttestationTag(t, ref, dsseMediaType, env)

	d := &OCIDiscoverer{}
	discovery, err := d.Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Len(t, discovery.Attestations, 1)

	att := discovery.Attestations[0]
	require.Equal(t, SourceAttestationTag, att.Source)
	require.Equal(t, digestToAttestationTag(image.Digest(ref.DigestStr())), att.Location)
	require.Equal(t, testPredicateType, att.PredicateType)
	require.Equal(t, SignatureUnsigned, att.Status)
	require.Empty(t, att.Signers)
}

func TestDiscoverAttestationTagStatement(t *testing.T) {
	// kpromo v4.3.1 pre-releases wrote unsigned statements.
	ref := pushImage(t, newReferrersRepo(t))
	pushAttestationTag(t, ref, inTotoMediaType,
		statement(t, ref, "https://k8s.io/promo-tools/promotion/v1"))

	d := &OCIDiscoverer{}
	discovery, err := d.Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Len(t, discovery.Attestations, 1)

	att := discovery.Attestations[0]
	require.Equal(t, SourceAttestationTag, att.Source)
	require.Equal(t, "https://k8s.io/promo-tools/promotion/v1", att.PredicateType)
	require.Equal(t, SignatureUnsigned, att.Status)
}

func TestDiscoverCopiedAttestation(t *testing.T) {
	// A real provenance of the Security Profiles Operator staging image
	// sha256:d2a142f2…, attached to another image. It verifies with the
	// real trust root, but is not about this image.
	data, err := os.ReadFile(filepath.Join("testdata", "spo-provenance.sigstore.json"))
	require.NoError(t, err)

	ref := pushImage(t, newReferrersRepo(t))
	pushReferrer(t, ref, BundleArtifactType, BundleArtifactType, data)

	discovery, err := (&OCIDiscoverer{}).Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Len(t, discovery.Attestations, 1)

	att := discovery.Attestations[0]
	require.Equal(t, testPredicateType, att.PredicateType)
	require.Equal(t, SignatureVerified, att.Status)
	require.Equal(t, []string{
		"sigstore::https://accounts.google.com::sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com",
	}, att.Signers)
	require.False(t, att.SubjectMatches)
	require.Contains(t, discovery.Summary()[0], "the statement is not about "+ref.DigestStr())
}

func TestDiscoverSkipsMalformedReferrers(t *testing.T) {
	ref := pushImage(t, newReferrersRepo(t))
	pushReferrer(t, ref, BundleArtifactType, BundleArtifactType,
		sigstoreBundle(t, statement(t, ref, testPredicateType)))
	bad := pushReferrer(t, ref, BundleArtifactType, BundleArtifactType, []byte(`{"mediaType": "x"}`))
	idx := pushIndexReferrer(t, ref, "application/vnd.example.index")

	d := &OCIDiscoverer{verifyFn: verifiedAs("a@example.com")}
	discovery, err := d.Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Len(t, discovery.Attestations, 1)
	require.True(t, discovery.Attestations[0].SubjectMatches)
	require.Equal(t, []Referrer{{
		Digest:       ref.DigestStr(),
		Location:     idx.String(),
		ArtifactType: "application/vnd.example.index",
	}}, discovery.Referrers)
	require.Len(t, discovery.Errors, 1)
	require.Contains(t, discovery.Errors[0], bad.String())
}

func TestDiscoverReferrersTagFallback(t *testing.T) {
	// Without the referrers API, referrers are listed in a tag.
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)

	repo, err := name.NewRepository(s.Listener.Addr().String() + "/test/image")
	require.NoError(t, err)

	ref := pushImage(t, repo)
	pushReferrer(t, ref, BundleArtifactType, BundleArtifactType,
		sigstoreBundle(t, statement(t, ref, testPredicateType)))

	d := &OCIDiscoverer{verifyFn: verifiedAs("a@example.com")}
	discovery, err := d.Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Len(t, discovery.Attestations, 1)
}

func TestDiscoverVerifyError(t *testing.T) {
	ref := pushImage(t, newReferrersRepo(t))
	pushReferrer(t, ref, BundleArtifactType, BundleArtifactType,
		sigstoreBundle(t, statement(t, ref, testPredicateType)))

	d := &OCIDiscoverer{verifyFn: func(attestation.Envelope) error {
		return errors.New("trust root unavailable")
	}}
	discovery, err := d.Discover(context.Background(), ref.String())
	require.NoError(t, err)
	require.Len(t, discovery.Attestations, 1)
	require.Equal(t, SignatureUnverifiable, discovery.Attestations[0].Status)
	require.Equal(t, "trust root unavailable", discovery.Attestations[0].Error)
	require.Empty(t, discovery.Attestations[0].Signers)
}

func TestDiscoverErrors(t *testing.T) {
	repo := newReferrersRepo(t)
	d := &OCIDiscoverer{}

	_, err := d.Discover(context.Background(), repo.Tag("latest").String())
	require.Error(t, err)

	_, err = d.Discover(context.Background(),
		repo.Digest("sha256:"+strings.Repeat("a", 64)).String())
	require.Error(t, err)
}

// recordedEnvelope is an envelope with a fixed verification.
type recordedEnvelope struct {
	attestation.Envelope

	verification attestation.Verification
	signatures   []attestation.Signature
}

//nolint:ireturn // implements attestation.Envelope
func (e *recordedEnvelope) GetVerification() attestation.Verification { return e.verification }

func (e *recordedEnvelope) GetSignatures() []attestation.Signature { return e.signatures }

func TestVerificationResult(t *testing.T) {
	signed := []attestation.Signature{nil}

	for _, tc := range []struct {
		name         string
		verification attestation.Verification
		signatures   []attestation.Signature
		status       SignatureStatus
		signers      []string
		reason       string
	}{
		{
			name: "verified",
			verification: &sapi.Verification{Signature: &sapi.SignatureVerification{
				Status:   sapi.VerificationStatus_VERIFIED,
				Verified: true,
				Identities: []*sapi.Identity{
					{Sigstore: &sapi.IdentitySigstore{Issuer: testIssuer, Identity: "a@example.com"}},
					{Sigstore: &sapi.IdentitySigstore{Issuer: testIssuer, Identity: "a@example.com"}},
				},
			}},
			signatures: signed,
			status:     SignatureVerified,
			signers:    []string{"sigstore::https://accounts.google.com::a@example.com"},
		},
		{
			name: "verified status without success",
			verification: &sapi.Verification{Signature: &sapi.SignatureVerification{
				Status: sapi.VerificationStatus_VERIFIED,
			}},
			signatures: signed,
			status:     SignatureFailed,
			reason:     "verification recorded without success",
		},
		{
			name: "failed",
			verification: &sapi.Verification{Signature: &sapi.SignatureVerification{
				Status: sapi.VerificationStatus_FAILED,
				Error:  "bad signature",
			}},
			signatures: signed,
			status:     SignatureFailed,
			reason:     "bad signature",
		},
		{
			name: "unverifiable",
			verification: &sapi.Verification{Signature: &sapi.SignatureVerification{
				Status: sapi.VerificationStatus_UNVERIFIABLE,
				Error:  "no key",
			}},
			signatures: signed,
			status:     SignatureUnverifiable,
			reason:     "no key",
		},
		{
			name: "unsigned",
			verification: &sapi.Verification{Signature: &sapi.SignatureVerification{
				Status: sapi.VerificationStatus_UNSIGNED,
			}},
			status: SignatureUnsigned,
		},
		{
			name:   "nothing recorded, no signatures",
			status: SignatureUnsigned,
		},
		{
			name:       "nothing recorded, signed",
			signatures: signed,
			status:     SignatureUnverifiable,
			reason:     "no verification result recorded",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, signers, reason := verificationResult(&recordedEnvelope{
				verification: tc.verification,
				signatures:   tc.signatures,
			})
			require.Equal(t, tc.status, status)
			require.Equal(t, tc.signers, signers)
			require.Equal(t, tc.reason, reason)
		})
	}
}

func TestIsPlatformManifest(t *testing.T) {
	t.Parallel()

	attestation := map[string]string{dockerReferenceTypeAnnotation: dockerAttestationManifest}
	unknown := &v1.Platform{OS: unknownPlatform, Architecture: unknownPlatform}
	arm64 := &v1.Platform{OS: "linux", Architecture: "arm64"}

	for _, tc := range []struct {
		name string
		desc v1.Descriptor
		want bool
	}{
		{name: "image", desc: v1.Descriptor{MediaType: types.OCIManifestSchema1, Platform: arm64}, want: true},
		{name: "index", desc: v1.Descriptor{MediaType: types.OCIImageIndex}, want: true},
		{name: "docker image", desc: v1.Descriptor{MediaType: types.DockerManifestSchema2}, want: true},
		{name: "not a manifest", desc: v1.Descriptor{MediaType: types.OCILayer}},
		{
			name: "attestation manifest",
			desc: v1.Descriptor{MediaType: types.OCIManifestSchema1, Annotations: attestation, Platform: unknown},
		},
		{
			name: "annotated image with a real platform",
			desc: v1.Descriptor{MediaType: types.OCIManifestSchema1, Annotations: attestation, Platform: arm64},
			want: true,
		},
		{
			name: "annotated image without a platform",
			desc: v1.Descriptor{MediaType: types.OCIManifestSchema1, Annotations: attestation},
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, IsPlatformManifest(&tc.desc))
		})
	}
}
