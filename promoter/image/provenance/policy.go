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
	"slices"
	"strings"

	sapi "github.com/carabiner-dev/signer/api/v1"
	"github.com/sirupsen/logrus"
)

// PolicyMode says what happens when an image does not satisfy its policy.
type PolicyMode string

const (
	// PolicyModeOff disables the policy. Images are verified as without a
	// policy: attestations are checked if present.
	PolicyModeOff PolicyMode = "off"

	// PolicyModeWarn evaluates the policy and logs violations without
	// blocking the promotion.
	PolicyModeWarn PolicyMode = "warn"

	// PolicyModeRequire blocks the promotion of images that do not
	// satisfy the policy.
	PolicyModeRequire PolicyMode = "require"
)

// minBuilderLevel, minPolicyLevel and maxLevel bound the SLSA build levels
// of builders and policies. The verifier treats controls above the
// required level as informative, and the trusted builder check is a level
// 2 control, so a lower policy level would stop enforcing the builders.
const (
	minBuilderLevel = 1
	minPolicyLevel  = 2
	maxLevel        = 3
)

// Builder is a trusted builder of SLSA build provenance and the SLSA build
// level it reaches. The level depends on how the builder isolates builds
// and who generates and signs the provenance, which the provenance can't
// show, so the policy states it. The builder ID is what the provenance
// claims, and any trusted signer can claim any trusted builder, so
// provenance verifies at no more than the lowest level of the builders of
// its policy.
type Builder struct {
	// ID is the builder ID of the provenance. An ID without an @ also
	// matches the builder at any ref.
	ID string `yaml:"id"`

	// Level is the SLSA build level the builder reaches, 1 to 3.
	// Provenance of the builder never verifies at a higher level.
	Level int `yaml:"level"`
}

// String describes the builder for the logs.
func (b *Builder) String() string {
	return fmt.Sprintf("%s (level %d)", b.ID, b.Level)
}

// UnmarshalYAML rejects builders given as plain IDs, without a level.
func (b *Builder) UnmarshalYAML(unmarshal func(any) error) error {
	var id string
	if err := unmarshal(&id); err == nil {
		return fmt.Errorf("provenance: builder %q needs a level, as {id: %q, level: <1-3>}", id, id)
	}

	type plain Builder

	return unmarshal((*plain)(b))
}

// Policy is the provenance policy of a project, declared in the
// `provenance` section of its promoter manifest. Images of the project
// satisfy it when they carry SLSA build provenance that is about the
// image digest, signed by one of the signers, built by one of the
// builders from one of the sources, and every required predicate type is
// attested by one of the signers.
type Policy struct {
	// Mode is off (the default), warn or require.
	Mode PolicyMode `yaml:"mode,omitempty"`

	// Signers are the identities trusted to sign the staging attestations,
	// written as signer identity specs, for example
	// "sigstore::https://accounts.google.com::builder@project.iam.gserviceaccount.com".
	// They are separate from the identity the promoter signs with.
	Signers []string `yaml:"signers,omitempty"`

	// Builders are the trusted builders of the SLSA build provenance.
	Builders []Builder `yaml:"builders,omitempty"`

	// Sources are the repositories the images may be built from, for
	// example "github.com/kubernetes-sigs/security-profiles-operator".
	Sources []string `yaml:"sources,omitempty"`

	// PredicateTypes are predicate types that must be attested for every
	// image, in addition to the build provenance.
	PredicateTypes []string `yaml:"predicateTypes,omitempty"`

	// Level is the SLSA build level the provenance must reach, 2 or 3, at
	// most the lowest level of the builders. Zero (the default) requires
	// every applicable SLSA build control to pass.
	Level int `yaml:"level,omitempty"`
}

// Enabled reports whether the policy is set and not off.
func (p *Policy) Enabled() bool {
	return p != nil && p.Mode != "" && p.Mode != PolicyModeOff
}

// Validate checks the policy.
func (p *Policy) Validate() error {
	if p == nil {
		return nil
	}

	var errs []error

	switch p.Mode {
	case "", PolicyModeOff, PolicyModeWarn, PolicyModeRequire:
	default:
		errs = append(errs, fmt.Errorf(
			"provenance: mode must be %q, %q or %q, got %q",
			PolicyModeOff, PolicyModeWarn, PolicyModeRequire, p.Mode,
		))
	}

	if _, err := p.identities(); err != nil {
		errs = append(errs, err)
	}

	for _, builder := range p.Builders {
		if strings.TrimSpace(builder.ID) == "" {
			errs = append(errs, errors.New("provenance: builder IDs must not be empty"))
		}

		if builder.Level < minBuilderLevel || builder.Level > maxLevel {
			errs = append(errs, fmt.Errorf(
				"provenance: builder %q needs a level between %d and %d, got %d",
				builder.ID, minBuilderLevel, maxLevel, builder.Level,
			))
		}
	}

	for _, source := range p.Sources {
		if strings.TrimSpace(source) == "" {
			errs = append(errs, errors.New("provenance: sources must not be empty"))
		}

		if strings.Contains(source, "@") {
			errs = append(errs, fmt.Errorf("provenance: source %q must not carry a ref", source))
		}
	}

	for _, predicateType := range p.PredicateTypes {
		if strings.TrimSpace(predicateType) == "" {
			errs = append(errs, errors.New("provenance: predicateTypes must not be empty"))
		}
	}

	if p.Level != 0 && (p.Level < minPolicyLevel || p.Level > maxLevel) {
		errs = append(errs, fmt.Errorf(
			"provenance: level must be between %d and %d, got %d", minPolicyLevel, maxLevel, p.Level,
		))
	}

	if lowest := p.builderLevel(); p.Level > 0 && lowest > 0 && p.Level > lowest {
		errs = append(errs, fmt.Errorf(
			"provenance: level %d is above %d, the lowest level of the builders", p.Level, lowest,
		))
	}

	if !p.Enabled() && (len(p.Signers) > 0 || len(p.Builders) > 0 || len(p.Sources) > 0 ||
		len(p.PredicateTypes) > 0 || p.Level > 0) {
		logrus.Warnf("provenance: the policy is %s, set a mode to check it", PolicyModeOff)
	}

	if p.Enabled() {
		if len(p.Signers) == 0 {
			errs = append(errs, errors.New("provenance: at least one signer is required"))
		}

		if len(p.Builders) == 0 {
			errs = append(errs, errors.New("provenance: at least one builder is required"))
		}

		if len(p.Sources) == 0 {
			errs = append(errs, errors.New("provenance: at least one source is required"))
		}
	}

	return errors.Join(errs...)
}

// Equal reports whether both policies declare the same expectations.
func (p *Policy) Equal(other *Policy) bool {
	if !p.Enabled() || !other.Enabled() {
		return p.Enabled() == other.Enabled()
	}

	return p.Mode == other.Mode &&
		sameElements(p.Signers, other.Signers) &&
		sameElements(builderStrings(p.Builders), builderStrings(other.Builders)) &&
		sameElements(p.Sources, other.Sources) &&
		sameElements(p.PredicateTypes, other.PredicateTypes) &&
		p.Level == other.Level
}

// builderStrings describes the builders, one per entry.
func builderStrings(builders []Builder) []string {
	strs := make([]string, 0, len(builders))
	for i := range builders {
		strs = append(strs, builders[i].String())
	}

	return strs
}

// sameElements reports whether two lists hold the same elements, in any
// order.
func sameElements(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// String describes the effective policy in one line, for the logs.
func (p *Policy) String() string {
	if !p.Enabled() {
		return string(PolicyModeOff)
	}

	parts := []string{
		"mode=" + string(p.Mode),
		"signers=[" + strings.Join(p.Signers, ", ") + "]",
		"builders=[" + strings.Join(builderStrings(p.Builders), ", ") + "]",
		"sources=[" + strings.Join(p.Sources, ", ") + "]",
	}

	if len(p.PredicateTypes) > 0 {
		parts = append(parts, "predicateTypes=["+strings.Join(p.PredicateTypes, ", ")+"]")
	}

	if p.Level > 0 {
		parts = append(parts, fmt.Sprintf("level=%d", p.Level))
	}

	return strings.Join(parts, " ")
}

// builderIDs returns the IDs of the builders.
func (p *Policy) builderIDs() []string {
	ids := make([]string, 0, len(p.Builders))
	for i := range p.Builders {
		ids = append(ids, p.Builders[i].ID)
	}

	return ids
}

// builderLevel returns the lowest SLSA build level of the builders, or
// zero without builders.
func (p *Policy) builderLevel() int {
	level := 0

	for i := range p.Builders {
		if level == 0 || p.Builders[i].Level < level {
			level = p.Builders[i].Level
		}
	}

	return level
}

// identities parses the signer identity specs.
func (p *Policy) identities() ([]*sapi.Identity, error) {
	var (
		ids  []*sapi.Identity
		errs []error
	)

	for _, spec := range p.Signers {
		id, err := sapi.NewIdentityFromSpec(spec)
		if err == nil {
			err = id.Validate()
		}

		if err == nil {
			err = checkSigstoreIdentity(id)
		}

		if err != nil {
			errs = append(errs, fmt.Errorf("provenance: invalid signer %q: %w", spec, err))

			continue
		}

		ids = append(ids, id)
	}

	return ids, errors.Join(errs...)
}

// checkSigstoreIdentity makes sure that a signer is a sigstore identity that
// names both the issuer and the identity. Discovery verifies sigstore
// signatures only, so other signers could never match, and a signer without
// an identity would trust every account of the issuer.
func checkSigstoreIdentity(id *sapi.Identity) error {
	sigstore := id.GetSigstore()
	if sigstore == nil {
		return errors.New("only sigstore signers are supported")
	}

	if sigstore.GetIssuer() == "" && matcherValue(sigstore.GetIssuerMatch()) == "" {
		return errors.New("the issuer must not be empty")
	}

	if sigstore.GetIdentity() == "" && matcherValue(sigstore.GetIdentityMatch()) == "" {
		return errors.New("the identity must not be empty")
	}

	return nil
}

// matcherValue returns the value of a string matcher, whatever its kind.
func matcherValue(m *sapi.StringMatcher) string {
	return m.GetExact() + m.GetRegex() + m.GetPrefix() + m.GetGlob()
}
