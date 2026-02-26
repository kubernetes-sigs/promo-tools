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

	checkresults "sigs.k8s.io/promo-tools/v4/promoter/image/checkresults"
	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
)

func TestFixMissingSignaturesSkipsEmptyResults(t *testing.T) {
	di := &DefaultPromoterImplementation{}
	opts := &options.Options{}

	// An entry with both Signed and Missing empty must not panic.
	results := checkresults.Signature{
		"example.com/image:v1": checkresults.CheckList{
			Signed:  []string{},
			Missing: []string{},
		},
	}

	if err := di.FixMissingSignatures(opts, results); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFixMissingSignaturesSkipsSigned(t *testing.T) {
	di := &DefaultPromoterImplementation{}
	opts := &options.Options{}

	// An entry with Signed populated should be skipped entirely
	// (handled by FixPartialSignatures instead).
	results := checkresults.Signature{
		"example.com/image:v1": checkresults.CheckList{
			Signed:  []string{"mirror1/image:sha256-abc.sig"},
			Missing: []string{"mirror2/image:sha256-abc.sig"},
		},
	}

	if err := di.FixMissingSignatures(opts, results); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
