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
	"testing"

	"github.com/stretchr/testify/require"
	grafeaspb "google.golang.org/genproto/googleapis/grafeas/v1"
)

func TestNoopScanner(t *testing.T) {
	s := &NoopScanner{}
	result, err := s.Scan(context.Background(), "gcr.io/test/image@sha256:abc")
	require.NoError(t, err)
	require.Equal(t, "gcr.io/test/image@sha256:abc", result.Reference)
	require.Empty(t, result.Vulnerabilities)
}

func TestScanResultExceedsSeverity(t *testing.T) {
	tests := []struct {
		name      string
		highest   Severity
		threshold Severity
		want      bool
	}{
		{"critical exceeds high", SeverityCritical, SeverityHigh, true},
		{"high equals high", SeverityHigh, SeverityHigh, true},
		{"medium below high", SeverityMedium, SeverityHigh, false},
		{"unspecified below low", SeverityUnspecified, SeverityLow, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ScanResult{HighestSeverity: tt.highest}
			require.Equal(t, tt.want, r.ExceedsSeverity(tt.threshold))
		})
	}
}

func TestSeverityOrdering(t *testing.T) {
	require.Less(t, SeverityMinimal, SeverityLow)
	require.Less(t, SeverityLow, SeverityMedium)
	require.Less(t, SeverityMedium, SeverityHigh)
	require.Less(t, SeverityHigh, SeverityCritical)
}

func TestParseProjectID(t *testing.T) {
	tests := []struct {
		ref     string
		want    string
		wantErr bool
	}{
		{"gcr.io/my-project/image@sha256:abc", "my-project", false},
		{"us-docker.pkg.dev/my-project/repo/image@sha256:abc", "my-project", false},
		{"gcr.io/k8s-staging-foo/bar:latest", "k8s-staging-foo", false},
		{"singlepart", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got, err := parseProjectID(tt.ref)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestMapGrafeasSeverity(t *testing.T) {
	tests := []struct {
		input grafeaspb.Severity
		want  Severity
	}{
		{grafeaspb.Severity_MINIMAL, SeverityMinimal},
		{grafeaspb.Severity_LOW, SeverityLow},
		{grafeaspb.Severity_MEDIUM, SeverityMedium},
		{grafeaspb.Severity_HIGH, SeverityHigh},
		{grafeaspb.Severity_CRITICAL, SeverityCritical},
		{grafeaspb.Severity_SEVERITY_UNSPECIFIED, SeverityUnspecified},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, mapGrafeasSeverity(tt.input))
	}
}

// Verify interface compliance at compile time.
var (
	_ Scanner = &NoopScanner{}
	_ Scanner = &GrafeasScanner{}
)
