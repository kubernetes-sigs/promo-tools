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
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// intotoStatementType is the in-toto statement media type.
	intotoStatementType = "https://in-toto.io/Statement/v1"

	// PredicateType is the predicate type for promotion provenance
	// attestations. Using a custom type (rather than the generic SLSA
	// provenance type) makes it easy to distinguish promoter attestations
	// from build-time attestations in the same .att image.
	PredicateType = "https://k8s.io/promo-tools/promotion/v1"
)

// PromotionGenerator implements Generator by producing SLSA v1.0
// provenance attestations for image promotions.
type PromotionGenerator struct{}

// Generate creates an in-toto statement with a SLSA v1.0 provenance
// predicate describing the promotion action.
func (g *PromotionGenerator) Generate(_ context.Context, record *PromotionRecord) ([]byte, error) {
	stmt := intotoStatement{
		Type:          intotoStatementType,
		PredicateType: PredicateType,
		Subject: []subject{
			{
				Name: record.DstRef,
				Digest: map[string]string{
					"sha256": trimDigestPrefix(record.Digest),
				},
			},
		},
		Predicate: slsaPredicate{
			BuildDefinition: buildDefinition{
				BuildType: "https://k8s.io/promo-tools/promotion/v1",
				ExternalParameters: map[string]string{
					"source":      record.SrcRef,
					"destination": record.DstRef,
				},
			},
			RunDetails: runDetails{
				Builder: builder{
					ID: record.BuilderID,
				},
				Metadata: metadata{
					StartedOn: record.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
				},
			},
		},
	}

	data, err := json.Marshal(stmt)
	if err != nil {
		return nil, fmt.Errorf("marshaling provenance statement: %w", err)
	}

	return data, nil
}

// trimDigestPrefix removes the "sha256:" prefix from a digest string.
func trimDigestPrefix(digest string) string {
	return strings.TrimPrefix(digest, "sha256:")
}

// intotoStatement is a minimal in-toto v1 statement.
type intotoStatement struct {
	Type          string        `json:"_type"` //nolint:tagliatelle // in-toto spec field
	PredicateType string        `json:"predicateType"`
	Subject       []subject     `json:"subject"`
	Predicate     slsaPredicate `json:"predicate"`
}

type subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// slsaPredicate is a minimal SLSA v1.0 provenance predicate.
type slsaPredicate struct {
	BuildDefinition buildDefinition `json:"buildDefinition"`
	RunDetails      runDetails      `json:"runDetails"`
}

type buildDefinition struct {
	BuildType          string            `json:"buildType"`
	ExternalParameters map[string]string `json:"externalParameters"`
}

type runDetails struct {
	Builder  builder  `json:"builder"`
	Metadata metadata `json:"metadata"`
}

type builder struct {
	ID string `json:"id"`
}

type metadata struct {
	StartedOn string `json:"startedOn"`
}
