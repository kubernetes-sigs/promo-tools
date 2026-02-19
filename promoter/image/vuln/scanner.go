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

// Package vuln provides interfaces and implementations for scanning
// container images for vulnerabilities before or after promotion.
// This replaces the GCP-specific Container Analysis (Grafeas) check
// with a portable interface.
package vuln

import (
	"context"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

// Severity represents the severity level of a vulnerability.
type Severity int

const (
	SeverityUnspecified Severity = iota
	SeverityMinimal
	SeverityLow
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

// Scanner checks container images for known vulnerabilities.
//
//counterfeiter:generate . Scanner
type Scanner interface {
	// Scan analyzes the image at the given reference for vulnerabilities.
	// The reference should be a fully qualified image reference including
	// a digest (e.g., "gcr.io/staging/image@sha256:abc...").
	Scan(ctx context.Context, ref string) (*ScanResult, error)
}

// ScanResult holds the outcome of a vulnerability scan.
type ScanResult struct {
	// Reference is the image that was scanned.
	Reference string

	// Vulnerabilities is the list of found vulnerabilities.
	Vulnerabilities []Vulnerability

	// HighestSeverity is the highest severity found across all vulnerabilities.
	HighestSeverity Severity
}

// Vulnerability represents a single CVE or security issue.
type Vulnerability struct {
	// ID is the vulnerability identifier (e.g., "CVE-2024-1234").
	ID string

	// Severity is the severity level.
	Severity Severity

	// Package is the affected package name.
	Package string

	// InstalledVersion is the currently installed version.
	InstalledVersion string

	// FixedVersion is the version that fixes this vulnerability, if available.
	FixedVersion string

	// Description is a short description of the vulnerability.
	Description string
}

// ExceedsSeverity returns true if the scan result contains any vulnerability
// at or above the given severity threshold.
func (r *ScanResult) ExceedsSeverity(threshold Severity) bool {
	return r.HighestSeverity >= threshold
}
