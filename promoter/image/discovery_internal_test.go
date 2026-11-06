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

package imagepromoter

import (
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
)

func TestLogDiscoveryWarnings(t *testing.T) {
	t.Parallel()

	const ref = "gcr.io/staging/kube-apiserver@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	for _, tc := range []struct {
		name          string
		discovery     *provenance.Discovery
		withoutPolicy bool
		wantWarning   bool
	}{
		{
			name:          "nothing found without a policy",
			discovery:     &provenance.Discovery{Reference: ref},
			withoutPolicy: true,
			wantWarning:   true,
		},
		{
			// The policy reports the missing provenance.
			name:      "nothing found with a policy",
			discovery: &provenance.Discovery{Reference: ref},
		},
		{
			name: "referrer attestation without a policy",
			discovery: &provenance.Discovery{
				Reference:    ref,
				Attestations: []provenance.Attestation{{Digest: "sha256:aaaa", Source: provenance.SourceReferrer}},
			},
			withoutPolicy: true,
		},
		{
			// The verify-if-present check verifies the tag.
			name:          "legacy attestation tag without attestations",
			discovery:     &provenance.Discovery{Reference: ref, AttestationTag: provenance.AttestationTagPresent},
			withoutPolicy: true,
		},
		{
			// The skipped referrers are logged instead.
			name:          "unreadable referrers without a policy",
			discovery:     &provenance.Discovery{Reference: ref, Errors: []string{"sha256:aaaa referrer: malformed"}},
			withoutPolicy: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log, hook := logtest.NewNullLogger()
			logDiscovery(log, ref, tc.discovery, tc.withoutPolicy)

			warnings := 0

			for _, entry := range hook.AllEntries() {
				if entry.Level <= logrus.WarnLevel {
					warnings++
				}
			}

			if tc.wantWarning {
				require.Equal(t, 1, warnings)
			} else {
				require.Zero(t, warnings)
			}
		})
	}
}
