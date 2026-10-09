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
	"path"
	"slices"
	"strings"

	"github.com/policylabs/attestation"
	sapi "github.com/policylabs/signer/api/v1"
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
// claims. A signer that builders name may claim only the builders that
// name it, and the other signers only the builders that name no signers,
// so provenance verifies at no more than the lowest level of the builders
// its signer may claim.
type Builder struct {
	// ID is the builder ID of the provenance. An ID without an @ also
	// matches the builder at any ref.
	ID string `yaml:"id"`

	// Level is the SLSA build level the builder reaches, 1 to 3.
	// Provenance of the builder never verifies at a higher level.
	Level int `yaml:"level"`

	// Signers are the policy signers, as written there, that may claim
	// the builder, for example the identity of an isolated provenance
	// generator. Without them, the policy signers no builder names may.
	Signers []string `yaml:"signers,omitempty"`

	// Caller constrains the GitHub Actions run that produced the
	// provenance, for a builder that is a reusable workflow any workflow
	// could call.
	Caller *Caller `yaml:"caller,omitempty"`
}

// String describes the builder for the logs.
func (b *Builder) String() string {
	details := []string{fmt.Sprintf("level %d", b.Level)}

	if len(b.Signers) > 0 {
		details = append(details, "signers "+strings.Join(slices.Sorted(slices.Values(b.Signers)), ", "))
	}

	if caller := b.Caller.String(); caller != "" {
		details = append(details, "caller "+caller)
	}

	return fmt.Sprintf("%s (%s)", b.ID, strings.Join(details, ", "))
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

// ImageLevel requires a SLSA build level for some images of a policy.
type ImageLevel struct {
	// Images are patterns of the image names, as written in the image
	// list of the manifest, relative to the source registry of the
	// policy, for example "spoc" or "charts/*". They use path.Match
	// syntax, so * doesn't match a /.
	Images []string `yaml:"images"`

	// Level is the SLSA build level the images must verify at, 2 or 3.
	Level int `yaml:"level"`
}

// String describes the image level for the logs.
func (l *ImageLevel) String() string {
	images := slices.Sorted(slices.Values(l.Images))

	return fmt.Sprintf("%s (level %d)", strings.Join(images, ", "), l.Level)
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
	// most the highest level provenance can verify at with the builders.
	// Zero (the default) requires every applicable SLSA build control to
	// pass.
	Level int `yaml:"level,omitempty"`

	// Levels require a SLSA build level for some images. An image whose
	// highest verified level is below the highest level of the entries
	// that match it doesn't satisfy the policy. Its provenance at lower
	// levels still counts, so it is carried all the same.
	Levels []ImageLevel `yaml:"levels,omitempty"`
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

	errs = append(errs, p.validateBuilders()...)

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

	if highest := p.highestLevel(); p.Level > 0 && highest > 0 && p.Level > highest {
		errs = append(errs, fmt.Errorf(
			"provenance: level %d is above %d, the highest level provenance can verify at with these builders",
			p.Level, highest,
		))
	}

	errs = append(errs, p.validateLevels()...)

	if !p.Enabled() && (len(p.Signers) > 0 || len(p.Builders) > 0 || len(p.Sources) > 0 ||
		len(p.PredicateTypes) > 0 || p.Level > 0 || len(p.Levels) > 0) {
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
		p.Level == other.Level &&
		maps.Equal(levelsByPattern(p.Levels), levelsByPattern(other.Levels))
}

// levelsByPattern returns the highest level the image levels require per
// pattern, so that equal requirements compare equal however the entries
// group the patterns.
func levelsByPattern(levels []ImageLevel) map[string]int {
	byPattern := make(map[string]int)

	for i := range levels {
		for _, pattern := range levels[i].Images {
			byPattern[pattern] = max(byPattern[pattern], levels[i].Level)
		}
	}

	return byPattern
}

// levelStrings describes the image levels, one per entry.
func levelStrings(levels []ImageLevel) []string {
	strs := make([]string, 0, len(levels))
	for i := range levels {
		strs = append(strs, levels[i].String())
	}

	return strs
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

	if len(p.Levels) > 0 {
		parts = append(parts, "levels=["+strings.Join(levelStrings(p.Levels), "; ")+"]")
	}

	return strings.Join(parts, " ")
}

// RequiredLevel returns the SLSA build level the policy requires for the
// image, named relative to the source registry of the policy: the highest
// level of the levels that match it, or zero if none does. An image at the
// source registry itself has no name, which no pattern is meant to match.
func (p *Policy) RequiredLevel(image string) int {
	if p == nil {
		return 0
	}

	required := 0

	for i := range p.Levels {
		if slices.ContainsFunc(p.Levels[i].Images, func(pattern string) bool {
			return matchesImage(pattern, image)
		}) {
			required = max(required, p.Levels[i].Level)
		}
	}

	return required
}

// UnmatchedLevelPatterns returns the patterns of the levels that match none
// of the images, named relative to the source registry of the policy. Such
// a pattern most likely has a typo, and requires nothing.
func (p *Policy) UnmatchedLevelPatterns(images []string) []string {
	if p == nil {
		return nil
	}

	var unmatched []string

	for i := range p.Levels {
		for _, pattern := range p.Levels[i].Images {
			if !slices.Contains(unmatched, pattern) && !slices.ContainsFunc(images, func(image string) bool {
				return matchesImage(pattern, image)
			}) {
				unmatched = append(unmatched, pattern)
			}
		}
	}

	return unmatched
}

// matchesImage reports whether the levels pattern matches the image name.
func matchesImage(pattern, image string) bool {
	image = strings.Trim(image, "/")
	if image == "" {
		return false
	}

	matched, err := path.Match(pattern, image)

	return err == nil && matched
}

// validateLevels checks the image levels of the policy.
func (p *Policy) validateLevels() []error {
	var errs []error

	highest := p.highestLevel()

	for i := range p.Levels {
		level := &p.Levels[i]

		if len(level.Images) == 0 {
			errs = append(errs, errors.New("provenance: every entry of levels needs at least one image"))
		}

		for _, pattern := range level.Images {
			if strings.TrimSpace(pattern) == "" {
				errs = append(errs, errors.New("provenance: image patterns of levels must not be empty"))

				continue
			}

			if _, err := path.Match(pattern, ""); err != nil {
				errs = append(errs, fmt.Errorf("provenance: invalid image pattern %q in levels: %w", pattern, err))
			}

			// Image names are relative to the source registry, so a
			// pattern with a leading or trailing / never matches.
			if strings.HasPrefix(pattern, "/") || strings.HasSuffix(pattern, "/") {
				errs = append(errs, fmt.Errorf(
					"provenance: image pattern %q in levels must not start or end with a /", pattern,
				))
			}
		}

		// Every satisfied image reaches level 1, so a lower entry would
		// require nothing.
		switch {
		case level.Level < minPolicyLevel || level.Level > maxLevel:
			errs = append(errs, fmt.Errorf(
				"provenance: levels of %s must be between %d and %d, got %d",
				strings.Join(level.Images, ", "), minPolicyLevel, maxLevel, level.Level,
			))
		case highest > 0 && level.Level > highest:
			errs = append(errs, fmt.Errorf(
				"provenance: level %d of %s is above %d, the highest level provenance can verify at with these builders",
				level.Level, strings.Join(level.Images, ", "), highest,
			))
		case p.Level > 0 && level.Level <= p.Level:
			logrus.Warnf(
				"provenance: level %d of %s requires nothing beyond the policy level %d",
				level.Level, strings.Join(level.Images, ", "), p.Level,
			)
		}
	}

	return errs
}

// validateBuilders checks the builders of the policy.
func (p *Policy) validateBuilders() []error {
	var errs []error

	for i, builder := range p.Builders {
		if strings.TrimSpace(builder.ID) == "" {
			errs = append(errs, errors.New("provenance: builder IDs must not be empty"))
		}

		if builder.Level < minBuilderLevel || builder.Level > maxLevel {
			errs = append(errs, fmt.Errorf(
				"provenance: builder %q needs a level between %d and %d, got %d",
				builder.ID, minBuilderLevel, maxLevel, builder.Level,
			))
		}

		for _, signer := range builder.Signers {
			if !slices.Contains(p.Signers, signer) {
				errs = append(errs, fmt.Errorf(
					"provenance: signer %q of builder %q must be one of the policy signers", signer, builder.ID,
				))
			}
		}

		if err := builder.Caller.validate(); err != nil {
			errs = append(errs, fmt.Errorf("provenance: builder %q: %w", builder.ID, err))
		}

		// A builder without signers that matches the ID of one with
		// signers would let other signers claim it.
		for _, other := range p.Builders[i+1:] {
			switch {
			case other.ID == builder.ID:
				errs = append(errs, fmt.Errorf("provenance: builder %q is listed more than once", builder.ID))
			case (len(builder.Signers) == 0) != (len(other.Signers) == 0) &&
				(coversBuilder(builder.ID, other.ID) || coversBuilder(other.ID, builder.ID)):
				errs = append(errs, fmt.Errorf(
					"provenance: builders %q and %q overlap, but only one of them names signers", builder.ID, other.ID,
				))
			}
		}
	}

	return errs
}

// coversBuilder reports whether the builder ID, without an @, also
// matches the other at a ref.
func coversBuilder(id, other string) bool {
	return !strings.Contains(id, "@") && strings.HasPrefix(other, id+"@")
}

// claimableBuilders returns the builders provenance signed by the given
// policy signers may claim: those that name one of them or, if none does,
// those that name no signers.
func (p *Policy) claimableBuilders(signers []string) []Builder {
	var bound, unbound []Builder

	for i := range p.Builders {
		builder := p.Builders[i]

		switch {
		case len(builder.Signers) == 0:
			unbound = append(unbound, builder)
		case slices.ContainsFunc(builder.Signers, func(s string) bool {
			return slices.Contains(signers, s)
		}):
			bound = append(bound, builder)
		}
	}

	if len(bound) > 0 {
		return bound
	}

	return unbound
}

// highestLevel returns the highest SLSA build level provenance can
// verify at under the policy, or zero without signers. Provenance whose
// signer matches several signers may verify at a lower level only.
func (p *Policy) highestLevel() int {
	highest := 0

	for _, signer := range p.Signers {
		highest = max(highest, lowestLevel(p.claimableBuilders([]string{signer})))
	}

	return highest
}

// matchingSigners returns the policy signers, as written there, that
// match one of the verified identities of a signature.
func (p *Policy) matchingSigners(verification attestation.Verification) []string {
	if verification == nil || !verification.GetVerified() {
		return nil
	}

	var signers []string

	for _, spec := range p.Signers {
		id, err := sapi.NewIdentityFromSpec(spec)
		if err != nil {
			continue
		}

		if verification.MatchesIdentity(id) {
			signers = append(signers, spec)
		}
	}

	return signers
}

// builderIDs returns the IDs of the builders.
func builderIDs(builders []Builder) []string {
	ids := make([]string, 0, len(builders))
	for i := range builders {
		ids = append(ids, builders[i].ID)
	}

	return ids
}

// lowestLevel returns the lowest SLSA build level of the builders, or
// zero without builders.
func lowestLevel(builders []Builder) int {
	level := 0

	for i := range builders {
		if level == 0 || builders[i].Level < level {
			level = builders[i].Level
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
