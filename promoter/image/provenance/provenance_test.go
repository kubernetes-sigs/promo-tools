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
	"testing"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

func TestNoopVerifier(t *testing.T) {
	v := &NoopVerifier{}
	result, err := v.Verify(context.Background(), "gcr.io/test/image@sha256:abc")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !result.Verified {
		t.Error("NoopVerifier should always return Verified=true")
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
var (
	_ Verifier = &NoopVerifier{}
	_ Verifier = &CosignVerifier{}
)
