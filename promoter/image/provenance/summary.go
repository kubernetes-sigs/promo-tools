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
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	vsav1 "github.com/in-toto/attestation/go/predicates/vsa/v1"
	intoto "github.com/in-toto/attestation/go/v1"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// SummaryPredicateType is the predicate type of SLSA verification
	// summary attestations.
	SummaryPredicateType = "https://slsa.dev/verification_summary/v1"

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

	// Verification results.
	resultPassed = "PASSED"
	resultFailed = "FAILED"

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

	// The summary claims a reviewed promoter manifest, so it needs one.
	if in.Policy.GetUri() == "" || in.Policy.GetDigest()["gitCommit"] == "" {
		return nil, errors.New("no promoter manifest at a commit")
	}

	algorithm, hex, ok := strings.Cut(in.Digest, ":")
	if !ok || hex == "" {
		return nil, fmt.Errorf("invalid digest %q", in.Digest)
	}

	result, levels := summaryResult(in.Sources)

	pred := &vsav1.VerificationSummary{
		Verifier:           &vsav1.VerificationSummary_Verifier{Id: SummaryVerifierID},
		TimeVerified:       timestamppb.New(in.Time),
		ResourceUri:        in.Name + "@" + in.Digest,
		InputAttestations:  inputAttestations(in.Sources),
		VerificationResult: result,
		VerifiedLevels:     levels,
		SlsaVersion:        summarySLSAVersion,
		Policy: &vsav1.VerificationSummary_Policy{
			Uri:    policyURI(in.Policy),
			Digest: in.Policy.GetDigest(),
		},
	}

	predicate, err := summaryPredicate(pred, in.Version)
	if err != nil {
		return nil, err
	}

	stmt := &intoto.Statement{
		Type: intotoStatementType,
		Subject: []*intoto.ResourceDescriptor{{
			Name:   in.Name,
			Digest: map[string]string{algorithm: hex},
		}},
		PredicateType: SummaryPredicateType,
		Predicate:     predicate,
	}

	data, err := protojson.Marshal(stmt)
	if err != nil {
		return nil, fmt.Errorf("marshaling verification summary: %w", err)
	}

	return data, nil
}

// ChildSummaryInput returns the input of the verification summary of a
// child of the indexes with the given inputs, whose digest the promoter
// manifests don't list. The provenance of an index is not about its
// children, so the summary of a child never claims a build level and has no
// input attestations. It fails when the summary of one of the indexes
// fails. Its policy is that of the first index, or the repository when the
// indexes come from different promoter manifests.
func ChildSummaryInput(digest string, indexes []*SummaryInput) *SummaryInput {
	if len(indexes) == 0 {
		return nil
	}

	first := indexes[0]
	outcome := &ImageProvenance{}
	policy := first.Policy

	for _, index := range indexes {
		if result, _ := summaryResult(index.Sources); result == resultFailed && len(outcome.Results) == 0 {
			outcome.Results = []*PolicyResult{{Violations: []string{"the verification of the index failed"}}}
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
		Sources: []SummarySource{{Provenance: outcome}},
	}
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
				return resultFailed, []string{levelFailed}
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

	return resultPassed, []string{build, LevelManifestReviewed}
}

// inputAttestations lists the attestations the policies of the source
// images accepted, once each. Referrers are identified by the digest of
// their bundle, at their location in staging: the digests stay the same
// when they are carried to the production registry. Attestations of
// legacy `.att` tags are identified by the digest of their payload.
func inputAttestations(sources []SummarySource) []*vsav1.VerificationSummary_InputAttestation {
	seen := map[*Attestation]bool{}

	var inputs []*vsav1.VerificationSummary_InputAttestation

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

			for _, att := range res.Accepted {
				if seen[att] {
					continue
				}

				seen[att] = true

				input := inputAttestation(repo, att)
				if input == nil {
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

// inputAttestation describes one input attestation, or returns nil if it
// can't be identified by a digest.
func inputAttestation(repo string, att *Attestation) *vsav1.VerificationSummary_InputAttestation {
	if att.Source == SourceReferrer {
		algorithm, hex, ok := strings.Cut(att.Layer, ":")
		if !ok {
			return nil
		}

		return &vsav1.VerificationSummary_InputAttestation{
			Uri:    repo + "@" + att.Location,
			Digest: map[string]string{algorithm: hex},
		}
	}

	if att.Envelope == nil || att.Envelope.GetPredicate() == nil ||
		att.Envelope.GetPredicate().GetOrigin() == nil ||
		len(att.Envelope.GetPredicate().GetOrigin().GetDigest()) == 0 {
		return nil
	}

	return &vsav1.VerificationSummary_InputAttestation{
		Uri:    repo + ":" + att.Location,
		Digest: att.Envelope.GetPredicate().GetOrigin().GetDigest(),
	}
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

// summaryPredicate renders the predicate as a struct and adds the verifier
// version, which the SLSA specification defines, but the in-toto types
// don't have yet.
func summaryPredicate(pred *vsav1.VerificationSummary, version string) (*structpb.Struct, error) {
	data, err := protojson.Marshal(pred)
	if err != nil {
		return nil, fmt.Errorf("marshaling verification summary predicate: %w", err)
	}

	predicate := &structpb.Struct{}
	if err := protojson.Unmarshal(data, predicate); err != nil {
		return nil, fmt.Errorf("unmarshaling verification summary predicate: %w", err)
	}

	verifier := predicate.GetFields()["verifier"].GetStructValue()
	if verifier == nil {
		return nil, errors.New("verification summary predicate without verifier")
	}

	verifier.Fields["version"] = structpb.NewStructValue(&structpb.Struct{
		Fields: map[string]*structpb.Value{"kpromo": structpb.NewStringValue(version)},
	})

	return predicate, nil
}
