/*
Copyright 2020 The Kubernetes Authors.

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

package inventory_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	grafeaspb "google.golang.org/genproto/googleapis/grafeas/v1"

	reg "sigs.k8s.io/promo-tools/v3/internal/legacy/dockerregistry"
	"sigs.k8s.io/promo-tools/v3/types/image"
)

// TestImageVulnCheck uses a fake populateRequests function and a fake
// vulnerability producer. The fake vulnerability producer simply returns the
// vulnerability occurrences that have been mapped to a given PromotionEdge in
// order to simulate running the real check without having to make an api call
// to the Container Analysis Service.
func TestImageVulnCheck(t *testing.T) {
	edge1 := reg.PromotionEdge{
		SrcImageTag: reg.ImageTag{
			Name: "foo",
		},
		Digest: "sha256:000",
		DstImageTag: reg.ImageTag{
			Name: "foo",
		},
	}
	edge2 := reg.PromotionEdge{
		SrcImageTag: reg.ImageTag{
			Name: "bar",
		},
		Digest: "sha256:111",
		DstImageTag: reg.ImageTag{
			Name: "bar/1",
		},
	}
	edge3 := reg.PromotionEdge{
		SrcImageTag: reg.ImageTag{
			Name: "bar",
		},
		Digest: "sha256:111",
		DstImageTag: reg.ImageTag{
			Name: "bar/2",
		},
	}

	mkVulnProducerFake := func(
		edgeVulnOccurrences map[image.Digest][]*grafeaspb.Occurrence,
	) reg.ImageVulnProducer {
		return func(
			edge reg.PromotionEdge,
		) ([]*grafeaspb.Occurrence, error) {
			return edgeVulnOccurrences[edge.Digest], nil
		}
	}

	tests := []struct {
		name              string
		severityThreshold int
		edges             map[reg.PromotionEdge]interface{}
		vulnerabilities   map[image.Digest][]*grafeaspb.Occurrence
		expected          error
	}{
		{
			"Severity under threshold",
			int(grafeaspb.Severity_HIGH),
			map[reg.PromotionEdge]interface{}{
				edge1: nil,
				edge2: nil,
			},
			map[image.Digest][]*grafeaspb.Occurrence{
				"sha256:000": {
					{
						Details: &grafeaspb.Occurrence_Vulnerability{
							Vulnerability: &grafeaspb.VulnerabilityOccurrence{
								Severity:     grafeaspb.Severity_LOW,
								FixAvailable: true,
							},
						},
					},
				},
				"sha256:111": {
					{
						Details: &grafeaspb.Occurrence_Vulnerability{
							Vulnerability: &grafeaspb.VulnerabilityOccurrence{
								Severity:     grafeaspb.Severity_MEDIUM,
								FixAvailable: true,
							},
						},
					},
				},
			},
			nil,
		},
		{
			"Severity at threshold",
			int(grafeaspb.Severity_HIGH),
			map[reg.PromotionEdge]interface{}{
				edge1: nil,
				edge2: nil,
			},
			map[image.Digest][]*grafeaspb.Occurrence{
				"sha256:000": {
					{
						Details: &grafeaspb.Occurrence_Vulnerability{
							Vulnerability: &grafeaspb.VulnerabilityOccurrence{
								Severity:     grafeaspb.Severity_HIGH,
								FixAvailable: true,
							},
						},
					},
				},
				"sha256:111": {
					{
						Details: &grafeaspb.Occurrence_Vulnerability{
							Vulnerability: &grafeaspb.VulnerabilityOccurrence{
								Severity:     grafeaspb.Severity_HIGH,
								FixAvailable: true,
							},
						},
					},
				},
			},
			fmt.Errorf("VulnerabilityCheck: The following vulnerable images were found:\n" +
				"    bar@sha256:111 [1 fixable severe vulnerabilities, 1 total]\n" +
				"    foo@sha256:000 [1 fixable severe vulnerabilities, 1 total]"),
		},
		{
			"Severity above threshold",
			int(grafeaspb.Severity_MEDIUM),
			map[reg.PromotionEdge]interface{}{
				edge1: nil,
				edge2: nil,
			},
			map[image.Digest][]*grafeaspb.Occurrence{
				"sha256:000": {
					{
						Details: &grafeaspb.Occurrence_Vulnerability{
							Vulnerability: &grafeaspb.VulnerabilityOccurrence{
								Severity:     grafeaspb.Severity_HIGH,
								FixAvailable: true,
							},
						},
					},
				},
				"sha256:111": {
					{
						Details: &grafeaspb.Occurrence_Vulnerability{
							Vulnerability: &grafeaspb.VulnerabilityOccurrence{
								Severity:     grafeaspb.Severity_CRITICAL,
								FixAvailable: true,
							},
						},
					},
					{
						Details: &grafeaspb.Occurrence_Vulnerability{
							Vulnerability: &grafeaspb.VulnerabilityOccurrence{
								Severity:     grafeaspb.Severity_HIGH,
								FixAvailable: true,
							},
						},
					},
				},
			},
			fmt.Errorf("VulnerabilityCheck: The following vulnerable images were found:\n" +
				"    bar@sha256:111 [2 fixable severe vulnerabilities, 2 total]\n" +
				"    foo@sha256:000 [1 fixable severe vulnerabilities, 1 total]"),
		},
		{
			"Multiple edges with same source image",
			int(grafeaspb.Severity_MEDIUM),
			map[reg.PromotionEdge]interface{}{
				edge2: nil,
				edge3: nil,
			},
			map[image.Digest][]*grafeaspb.Occurrence{
				"sha256:111": {
					{
						Details: &grafeaspb.Occurrence_Vulnerability{
							Vulnerability: &grafeaspb.VulnerabilityOccurrence{
								Severity:     grafeaspb.Severity_HIGH,
								FixAvailable: true,
							},
						},
					},
				},
			},
			fmt.Errorf("VulnerabilityCheck: The following vulnerable images were found:\n" +
				"    bar@sha256:111 [1 fixable severe vulnerabilities, 1 total]"),
		},
		{
			"Multiple vulnerabilities with no fix",
			int(grafeaspb.Severity_MEDIUM),
			map[reg.PromotionEdge]interface{}{
				edge1: nil,
			},
			map[image.Digest][]*grafeaspb.Occurrence{
				"sha256:000": {
					{
						Details: &grafeaspb.Occurrence_Vulnerability{
							Vulnerability: &grafeaspb.VulnerabilityOccurrence{
								Severity:     grafeaspb.Severity_HIGH,
								FixAvailable: false,
							},
						},
					},
					{
						Details: &grafeaspb.Occurrence_Vulnerability{
							Vulnerability: &grafeaspb.VulnerabilityOccurrence{
								Severity:     grafeaspb.Severity_CRITICAL,
								FixAvailable: false,
							},
						},
					},
				},
			},
			nil,
		},
	}

	for _, test := range tests {
		sc := reg.SyncContext{}
		check := reg.MKImageVulnCheck(
			&sc,
			test.edges,
			test.severityThreshold,
			mkVulnProducerFake(test.vulnerabilities),
		)
		got := check.Run()
		require.Equal(t, test.expected, got)
	}
}
