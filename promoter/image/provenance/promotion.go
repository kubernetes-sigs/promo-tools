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
	"fmt"
	"strings"

	intoto "github.com/in-toto/attestation/go/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
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

// PromotionGenerator implements Generator by producing in-toto
// provenance attestations for image promotions.
type PromotionGenerator struct{}

// Generate creates an in-toto statement with a promotion record
// predicate describing the promotion action.
func (g *PromotionGenerator) Generate(_ context.Context, record *PromotionRecord) ([]byte, error) {
	recordJSON, err := protojson.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("marshaling promotion record: %w", err)
	}

	predicate := &structpb.Struct{}
	if err := protojson.Unmarshal(recordJSON, predicate); err != nil {
		return nil, fmt.Errorf("unmarshaling predicate struct: %w", err)
	}

	stmt := &intoto.Statement{
		Type: intotoStatementType,
		Subject: []*intoto.ResourceDescriptor{
			{
				Name: record.GetDstRef(),
				Digest: map[string]string{
					"sha256": strings.TrimPrefix(record.GetDigest(), "sha256:"),
				},
			},
		},
		PredicateType: PredicateType,
		Predicate:     predicate,
	}

	data, err := protojson.Marshal(stmt)
	if err != nil {
		return nil, fmt.Errorf("marshaling provenance statement: %w", err)
	}

	return data, nil
}
