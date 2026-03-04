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
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

func TestPromotionGeneratorGenerate(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	record := &PromotionRecord{
		SrcRef:    "us.gcr.io/staging/myimage:v1.0",
		DstRef:    "us.gcr.io/production/myimage:v1.0",
		Digest:    "sha256:abc123",
		Timestamp: timestamppb.New(ts),
		BuilderId: "https://k8s.io/promo-tools@v4.0.8",
	}

	gen := &PromotionGenerator{}

	data, err := gen.Generate(context.Background(), record)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	var stmt map[string]any
	if err := json.Unmarshal(data, &stmt); err != nil {
		t.Fatalf("unmarshaling statement: %v", err)
	}

	// Verify top-level in-toto statement fields.
	if got := stmt["_type"]; got != intotoStatementType {
		t.Errorf("_type = %v, want %v", got, intotoStatementType)
	}

	if got := stmt["predicateType"]; got != PredicateType {
		t.Errorf("predicateType = %v, want %v", got, PredicateType)
	}

	// Verify subject contains destination ref with trimmed digest.
	subjects, ok := stmt["subject"].([]any)
	if !ok || len(subjects) != 1 {
		t.Fatalf("expected 1 subject, got %v", stmt["subject"])
	}

	subj, ok := subjects[0].(map[string]any)
	if !ok {
		t.Fatal("expected subject to be an object")
	}

	if got := subj["name"]; got != record.GetDstRef() {
		t.Errorf("subject name = %v, want %v", got, record.GetDstRef())
	}

	digest, ok := subj["digest"].(map[string]any)
	if !ok {
		t.Fatal("expected digest to be an object")
	}

	if got := digest["sha256"]; got != "abc123" {
		t.Errorf("subject digest sha256 = %v, want abc123", got)
	}

	// Verify predicate contains promotion record fields.
	predicate, ok := stmt["predicate"].(map[string]any)
	if !ok {
		t.Fatalf("expected predicate to be an object, got %T", stmt["predicate"])
	}

	if got := predicate["srcRef"]; got != record.GetSrcRef() {
		t.Errorf("predicate srcRef = %v, want %v", got, record.GetSrcRef())
	}

	if got := predicate["dstRef"]; got != record.GetDstRef() {
		t.Errorf("predicate dstRef = %v, want %v", got, record.GetDstRef())
	}

	if got := predicate["digest"]; got != record.GetDigest() {
		t.Errorf("predicate digest = %v, want %v", got, record.GetDigest())
	}

	if got := predicate["builderId"]; got != record.GetBuilderId() {
		t.Errorf("predicate builderId = %v, want %v", got, record.GetBuilderId())
	}

	if got := predicate["timestamp"]; got != "2026-03-04T12:00:00Z" {
		t.Errorf("predicate timestamp = %v, want 2026-03-04T12:00:00Z", got)
	}
}

func TestDigestToAttestationTag(t *testing.T) {
	tests := []struct {
		digest image.Digest
		want   string
	}{
		{
			"sha256:abc123",
			"sha256-abc123.att",
		},
		{
			"sha256:0000000000000000000000000000000000000000000000000000000000000000",
			"sha256-0000000000000000000000000000000000000000000000000000000000000000.att",
		},
	}

	for _, tt := range tests {
		got := digestToAttestationTag(tt.digest)
		if got != tt.want {
			t.Errorf("digestToAttestationTag(%q) = %q, want %q", tt.digest, got, tt.want)
		}
	}
}

// Verify interface compliance at compile time.
var _ Verifier = &CosignVerifier{}
