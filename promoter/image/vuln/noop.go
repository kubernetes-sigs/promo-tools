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
)

// NoopScanner always returns an empty scan result with no vulnerabilities.
// Used when vulnerability scanning is disabled.
type NoopScanner struct{}

// Scan returns a clean result.
func (n *NoopScanner) Scan(_ context.Context, ref string) (*ScanResult, error) {
	return &ScanResult{
		Reference: ref,
	}, nil
}
