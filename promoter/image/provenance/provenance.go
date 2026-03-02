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

// Package provenance provides interfaces and implementations for verifying
// the provenance of container images before promotion. This includes
// checking SLSA attestations, builder identity, and source repository
// against configurable policies.
package provenance

import (
	"context"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

// Verifier checks the provenance of a container image.
//
//counterfeiter:generate . Verifier
type Verifier interface {
	// Verify checks whether the image at the given reference has valid
	// provenance according to the configured policy. The reference
	// should be a fully qualified image reference including digest
	// (e.g., "gcr.io/staging/image@sha256:abc...").
	Verify(ctx context.Context, ref string) (*Result, error)
}

// Result describes the outcome of a provenance verification.
type Result struct {
	// Verified is true if the image has valid provenance.
	Verified bool

	// Builder is the identity of the build system that produced the image
	// (e.g., "https://cloudbuild.googleapis.com/GoogleHostedWorker@v0.3").
	Builder string

	// SourceRepo is the source repository URL from the provenance
	// (e.g., "https://github.com/kubernetes/kubernetes").
	SourceRepo string

	// Errors lists any issues found during verification.
	Errors []string
}
