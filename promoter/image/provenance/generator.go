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
	"time"
)

// Generator produces provenance attestations for promoted images.
//
//counterfeiter:generate . Generator
type Generator interface {
	// Generate creates an in-toto attestation envelope (JSON) for the
	// given promotion record.
	Generate(ctx context.Context, record *PromotionRecord) ([]byte, error)
}

// PromotionRecord captures the details of a single image promotion
// for provenance generation.
type PromotionRecord struct {
	// SrcRef is the fully qualified source image reference.
	SrcRef string

	// DstRef is the fully qualified destination image reference.
	DstRef string

	// Digest is the image digest (e.g., "sha256:abc...").
	Digest string

	// Timestamp is when the promotion occurred.
	Timestamp time.Time

	// BuilderID identifies the promotion system
	// (e.g., "https://k8s.io/promo-tools@v4.0.8").
	BuilderID string
}
