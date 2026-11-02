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
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	intoto "github.com/in-toto/attestation/go/v1"
	"github.com/sirupsen/logrus"
	slsavsa "github.com/slsa-framework/verifier/pkg/slsa/vsa"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	// SummaryPredicateType is the predicate type of SLSA verification
	// summary attestations.
	SummaryPredicateType = slsavsa.PredicateTypeV1

	// SummaryVerifierID identifies kpromo as the verifier of the
	// verification summaries it publishes. Consumers trust it together
	// with the identity that signs the summaries.
	SummaryVerifierID = "https://k8s.io/promo-tools/verifier/v1"

	// LevelBuildUnevaluated is the build level of an image whose build
	// provenance was not evaluated against a policy.
	LevelBuildUnevaluated = "SLSA_BUILD_LEVEL_UNEVALUATED"

	// LevelManifestReviewed says that the promotion of an image comes from
	// a reviewed promoter manifest.
	LevelManifestReviewed = "K8S_PROMOTION_MANIFEST_REVIEWED"

	// levelFailed is the only verified level of a failed verification.
	levelFailed = "FAILED"

	// summarySLSAVersion is the SLSA version whose criteria the build
	// provenance is evaluated against: the SLSA verifier's control
	// catalog implements the build track of SLSA 1.0.
	summarySLSAVersion = "1.0"

	// levelBuildPrefix prefixes the build level of a verification.
	levelBuildPrefix = "SLSA_BUILD_LEVEL_"
)

// SummaryInput is what a verification summary of one promoted digest is
// made of.
type SummaryInput struct {
	// Name is the production name of the image, for example
	// "registry.k8s.io/pause".
	Name string

	// Digest is the promoted digest, for example "sha256:abc...".
	Digest string

	// Version is the kpromo version.
	Version string

	// Time is when the image was verified.
	Time time.Time

	// Policy describes the reviewed promoter manifest the image was
	// promoted from, or the repository of the manifests at its commit when
	// the policies come from several manifests. It needs a URI and a
	// "gitCommit" digest.
	Policy *ResourceDescriptor

	// Sources are the staging images the digest was promoted from,
	// usually one.
	Sources []SummarySource
}

// SummarySource is a staging image a verification summary covers.
type SummarySource struct {
	// Discovery holds the attestations discovered for the image. Nil if
	// the discovery failed.
	Discovery *Discovery

	// Provenance is what the provenance phase concluded for the image.
	Provenance *ImageProvenance
}

// VerificationSummary returns the in-toto statement of a SLSA verification
// summary for a promoted digest:
//
//   - The verification passes when every provenance policy that applies to
//     the staging images is satisfied. It fails when one is not, which only
//     happens in warn mode, because require mode blocks the promotion. A
//     failed summary has the verified level "FAILED", as the SLSA
//     specification asks.
//   - A passed summary claims the lowest SLSA build level the policies
//     verified, or SLSA_BUILD_LEVEL_UNEVALUATED when a staging image had no
//     policy or a policy verified no level, and
//     K8S_PROMOTION_MANIFEST_REVIEWED, for which it needs the promoter
//     manifest at a commit.
//   - The input attestations are the attestations the policies accepted.
func VerificationSummary(in *SummaryInput) ([]byte, error) {
	if len(in.Sources) == 0 {
		return nil, errors.New("no source image")
	}

	for i := range in.Sources {
		if in.Sources[i].Provenance == nil {
			return nil, errors.New("no provenance result")
		}
	}

	if in.Time.IsZero() {
		return nil, errors.New("no verification time")
	}

	// The summary claims a reviewed promoter manifest, so it needs one.
	if in.Policy.GetUri() == "" || in.Policy.GetDigest()["gitCommit"] == "" {
		return nil, errors.New("no promoter manifest at a commit")
	}

	if !completeDigest(in.Policy.GetDigest()) {
		return nil, fmt.Errorf("incomplete digest of the promoter manifest %v", in.Policy.GetDigest())
	}

	algorithm, hex, ok := strings.Cut(in.Digest, ":")
	if !ok || !completeDigest(map[string]string{algorithm: hex}) {
		return nil, fmt.Errorf("invalid digest %q", in.Digest)
	}

	result, levels := summaryResult(in.Sources)

	// The SLSA verifier builds the statement. It rejects it for an empty
	// algorithm or value in the policy digest or the digest of an input
	// attestation, which the check above and inputAttestations rule out
	// before, see completeDigest.
	summary := &slsavsa.SummaryInput{
		VerifierID:   SummaryVerifierID,
		TimeVerified: in.Time,
		Subjects: []*intoto.ResourceDescriptor{{
			Name:   in.Name,
			Digest: map[string]string{algorithm: hex},
		}},
		ResourceURI:        in.Name + "@" + in.Digest,
		PolicyURI:          policyURI(in.Policy),
		PolicyDigest:       in.Policy.GetDigest(),
		InputAttestations:  inputAttestations(in.Sources),
		VerificationResult: result,
		VerifiedLevels:     levels,
		SLSAVersion:        summarySLSAVersion,
	}

	stmt, err := summary.Statement()
	if err != nil {
		return nil, fmt.Errorf("building verification summary: %w", err)
	}

	if err := addVerifierVersion(stmt.GetPredicate(), in.Version); err != nil {
		return nil, err
	}

	data, err := protojson.Marshal(stmt)
	if err != nil {
		return nil, fmt.Errorf("marshaling verification summary: %w", err)
	}

	return data, nil
}

// ChildSummaryInput returns the input of the verification summary of a
// platform manifest of the indexes with the given inputs, whose digest the
// promoter manifests don't list. Each source image of an index contributes
// the policy results of the platform manifest, evaluated against its own
// attestations, when they satisfy the policies, whether or not the index
// satisfied them on its own. Otherwise, because no policy applies or the
// platform manifest has no passing attestations of its own, it claims no
// build level and fails when the summary of its index fails. The policy is that of the first index, or the repository when the
// indexes come from different promoter manifests.
func ChildSummaryInput(digest string, indexes []*SummaryInput) *SummaryInput {
	if len(indexes) == 0 {
		return nil
	}

	first := indexes[0]
	policy := first.Policy

	var sources []SummarySource

	for _, index := range indexes {
		for i := range index.Sources {
			source := &index.Sources[i]

			platform := SummarySource{Discovery: source.Discovery, Provenance: source.Provenance.Platform(digest)}
			if platform.Provenance != nil {
				if result, _ := summaryResult([]SummarySource{platform}); result == slsavsa.ResultPassed {
					sources = append(sources, platform)

					continue
				}
			}

			outcome := &ImageProvenance{}
			if result, _ := summaryResult([]SummarySource{*source}); result == slsavsa.ResultFailed {
				outcome.Results = []*PolicyResult{{Violations: []string{"the verification of the index failed"}}}
			}

			sources = append(sources, SummarySource{Provenance: outcome})
		}

		if policyURI(index.Policy) != policyURI(first.Policy) {
			policy = &ResourceDescriptor{Uri: first.Policy.GetUri(), Digest: first.Policy.GetDigest()}
		}
	}

	return &SummaryInput{
		Name:    first.Name,
		Digest:  digest,
		Version: first.Version,
		Time:    first.Time,
		Policy:  policy,
		Sources: sources,
	}
}

// Platform returns the policy results of a platform manifest of the index
// this is the outcome for, or nil if the policies were not evaluated for
// it.
func (p *ImageProvenance) Platform(digest string) *ImageProvenance {
	if p == nil || len(p.Results) == 0 {
		return nil
	}

	platform := &ImageProvenance{Policies: p.Policies}

	for _, res := range p.Results {
		if res == nil || res.Platforms[digest] == nil {
			return nil
		}

		platform.Results = append(platform.Results, res.Platforms[digest])
	}

	return platform
}

// summaryResult returns the verification result and the verified levels
// of a digest, in this order. It fails if a policy of any source image is
// not satisfied. It claims the lowest build level the policies verified,
// and none if a source image had no policy or a policy verified no level.
func summaryResult(sources []SummarySource) (string, []string) {
	var (
		lowest    int
		evaluated = true
	)

	for i := range sources {
		results := sources[i].Provenance.Results
		if len(results) == 0 {
			evaluated = false
		}

		for _, res := range results {
			if res == nil || !res.Satisfied {
				return slsavsa.ResultFailed, []string{levelFailed}
			}

			// A level that was not verified must never be claimed.
			if res.SLSALevel < 1 {
				evaluated = false

				continue
			}

			if lowest == 0 || res.SLSALevel < lowest {
				lowest = res.SLSALevel
			}
		}
	}

	build := LevelBuildUnevaluated
	if evaluated && lowest > 0 {
		build = fmt.Sprintf("%s%d", levelBuildPrefix, lowest)
	}

	return slsavsa.ResultPassed, []string{build, LevelManifestReviewed}
}

// inputAttestations lists the attestations the policies of the source
// images accepted, once each. Referrers are identified by the digest of
// their bundle, at their location in staging: the digests stay the same
// when they are carried to the production registry. Attestations of
// legacy `.att` tags are identified by the digest of their payload.
func inputAttestations(sources []SummarySource) []slsavsa.InputAttestation {
	seen := map[*Attestation]bool{}

	var inputs []slsavsa.InputAttestation

	for i := range sources {
		discovery := sources[i].Discovery
		if discovery == nil {
			continue
		}

		repo := discovery.Reference
		if ref, err := name.NewDigest(discovery.Reference); err == nil {
			repo = ref.Context().Name()
		}

		for _, res := range sources[i].Provenance.Results {
			if res == nil {
				continue
			}

			// An index that satisfied the policy through its platform
			// manifests was verified with their attestations. The results
			// are shared between summaries written in parallel, so the
			// lists are copied, never appended to.
			accepted := res.Accepted
			if res.ThroughPlatforms {
				lists := [][]*Attestation{res.Accepted}
				for _, digest := range slices.Sorted(maps.Keys(res.Platforms)) {
					lists = append(lists, res.Platforms[digest].Accepted)
				}

				accepted = slices.Concat(lists...)
			}

			for _, att := range accepted {
				if seen[att] {
					continue
				}

				seen[att] = true

				input, ok := inputAttestation(repo, att)
				if !ok {
					logrus.Warnf("Unable to identify the %s attestation %s of %s in its verification summary",
						att.PredicateType, att.Location, repo)

					continue
				}

				inputs = append(inputs, input)
			}
		}
	}

	return inputs
}

// inputAttestation describes one input attestation, or returns false if it
// can't be identified by a digest. A digest with an empty algorithm or value
// doesn't identify it either, and the SLSA verifier would reject the whole
// summary for it.
func inputAttestation(repo string, att *Attestation) (slsavsa.InputAttestation, bool) {
	if att.Source == SourceReferrer {
		algorithm, hex, ok := strings.Cut(att.Layer, ":")
		digest := map[string]string{algorithm: hex}

		if !ok || !completeDigest(digest) {
			return slsavsa.InputAttestation{}, false
		}

		return slsavsa.InputAttestation{URI: repo + "@" + att.Location, Digest: digest}, true
	}

	if att.Envelope == nil || att.Envelope.GetPredicate() == nil {
		return slsavsa.InputAttestation{}, false
	}

	digest := att.Envelope.GetPredicate().GetOrigin().GetDigest()
	if !completeDigest(digest) {
		return slsavsa.InputAttestation{}, false
	}

	return slsavsa.InputAttestation{URI: repo + ":" + att.Location, Digest: digest}, true
}

// completeDigest reports whether a digest set has at least one digest and
// neither an empty algorithm nor an empty value. It is the rule the SLSA
// verifier checks the digests of input attestations against, which it
// doesn't export, so that kpromo can leave out an input attestation or fail
// with a clear error instead of the verifier rejecting the summary. kpromo
// checks the policy and the subject digest with it as well.
func completeDigest(digest map[string]string) bool {
	if len(digest) == 0 {
		return false
	}

	for algorithm, value := range digest {
		if algorithm == "" || value == "" {
			return false
		}
	}

	return true
}

// policyURI returns a stable URI of the promoter manifest: its repository
// and path, or only the repository, without the commit, which is the
// policy digest.
func policyURI(policy *ResourceDescriptor) string {
	if policy.GetName() == "" {
		return policy.GetUri()
	}

	return policy.GetUri() + "#" + policy.GetName()
}

// addVerifierVersion adds the kpromo version to the verifier of the
// predicate, which the SLSA specification defines, but the in-toto types,
// and so the SLSA verifier, don't have yet.
func addVerifierVersion(predicate *structpb.Struct, version string) error {
	verifier := predicate.GetFields()["verifier"].GetStructValue()
	if verifier == nil {
		return errors.New("verification summary predicate without verifier")
	}

	verifier.Fields["version"] = structpb.NewStructValue(&structpb.Struct{
		Fields: map[string]*structpb.Value{"kpromo": structpb.NewStringValue(version)},
	})

	return nil
}
