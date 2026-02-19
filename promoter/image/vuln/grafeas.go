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

package vuln

import (
	"context"
	"fmt"
	"strings"

	containeranalysis "cloud.google.com/go/containeranalysis/apiv1"
	"google.golang.org/api/iterator"
	grafeaspb "google.golang.org/genproto/googleapis/grafeas/v1"
)

// GrafeasScanner uses GCP Container Analysis (Grafeas) to scan images
// for vulnerabilities. This wraps the existing GCP-specific scanning
// capability behind the portable Scanner interface.
type GrafeasScanner struct {
	// FixableOnly when true only reports vulnerabilities that have a
	// fix available, matching the legacy behavior.
	FixableOnly bool
}

// Scan queries the Container Analysis API for vulnerability occurrences
// on the given image reference. The reference must be a GCR/Artifact
// Registry image (e.g., "gcr.io/project/image@sha256:...") so that
// the GCP project ID can be derived from the registry path.
func (s *GrafeasScanner) Scan(ctx context.Context, ref string) (*ScanResult, error) {
	projectID, err := parseProjectID(ref)
	if err != nil {
		return nil, fmt.Errorf("parsing project ID from %q: %w", ref, err)
	}

	client, err := containeranalysis.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating container analysis client: %w", err)
	}
	defer client.Close()

	// Build the resource URL for the image. Container Analysis expects
	// the full https:// URL form.
	resourceURL := "https://" + ref

	req := &grafeaspb.ListOccurrencesRequest{
		Parent: "projects/" + projectID,
		Filter: fmt.Sprintf("resourceUrl = %q kind = %q",
			resourceURL, "VULNERABILITY"),
	}

	result := &ScanResult{Reference: ref}
	it := client.GetGrafeasClient().ListOccurrences(ctx, req)
	for {
		occ, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing occurrences: %w", err)
		}

		vuln := occ.GetVulnerability()
		if vuln == nil {
			continue
		}

		// Match legacy behavior: only report fixable vulnerabilities
		// when FixableOnly is set.
		if s.FixableOnly && !vuln.GetFixAvailable() {
			continue
		}

		severity := mapGrafeasSeverity(vuln.GetSeverity())
		v := Vulnerability{
			ID:       occ.GetName(),
			Severity: severity,
		}

		// Extract package info from the first affected package, if available.
		if pkgs := vuln.GetPackageIssue(); len(pkgs) > 0 {
			pkg := pkgs[0]
			v.Package = pkg.GetAffectedPackage()
			if affected := pkg.GetAffectedVersion(); affected != nil {
				v.InstalledVersion = affected.GetFullName()
			}
			if fixed := pkg.GetFixedVersion(); fixed != nil {
				v.FixedVersion = fixed.GetFullName()
			}
		}

		v.Description = vuln.GetShortDescription()

		result.Vulnerabilities = append(result.Vulnerabilities, v)
		if severity > result.HighestSeverity {
			result.HighestSeverity = severity
		}
	}

	return result, nil
}

// parseProjectID extracts the GCP project ID from an image reference.
// For "gcr.io/my-project/image@sha256:...", returns "my-project".
// For "us-docker.pkg.dev/my-project/repo/image@sha256:...", returns "my-project".
func parseProjectID(ref string) (string, error) {
	// Strip digest or tag
	refBase := ref
	if idx := strings.Index(ref, "@"); idx != -1 {
		refBase = ref[:idx]
	} else if idx := strings.Index(ref, ":"); idx != -1 {
		refBase = ref[:idx]
	}

	parts := strings.Split(refBase, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("cannot extract project ID from %q: not enough path components", ref)
	}

	// The project ID is always the second path component
	// (after the registry hostname).
	return parts[1], nil
}

// mapGrafeasSeverity maps a Grafeas severity enum to our Severity type.
// The numeric values happen to align (both use 0-5), but we map explicitly
// for safety.
func mapGrafeasSeverity(s grafeaspb.Severity) Severity {
	switch s {
	case grafeaspb.Severity_MINIMAL:
		return SeverityMinimal
	case grafeaspb.Severity_LOW:
		return SeverityLow
	case grafeaspb.Severity_MEDIUM:
		return SeverityMedium
	case grafeaspb.Severity_HIGH:
		return SeverityHigh
	case grafeaspb.Severity_CRITICAL:
		return SeverityCritical
	default:
		return SeverityUnspecified
	}
}
