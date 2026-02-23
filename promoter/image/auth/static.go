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

package auth

import (
	"context"
	"fmt"
)

// StaticIdentityTokenProvider implements IdentityTokenProvider with a fixed token.
type StaticIdentityTokenProvider struct {
	// Token is returned for all GetIdentityToken calls.
	Token string

	// Err is returned for all GetIdentityToken calls if non-nil.
	Err error
}

// GetIdentityToken returns the static identity token.
func (s *StaticIdentityTokenProvider) GetIdentityToken(_ context.Context, _, _ string) (string, error) {
	if s.Err != nil {
		return "", fmt.Errorf("static identity token error: %w", s.Err)
	}
	return s.Token, nil
}

// NoopServiceActivator implements ServiceActivator as a no-op.
// Useful for testing or environments that don't need service account activation.
type NoopServiceActivator struct {
	// Err is returned for all ActivateServiceAccounts calls if non-nil.
	Err error
}

// ActivateServiceAccounts is a no-op.
func (n *NoopServiceActivator) ActivateServiceAccounts(_ context.Context, _ string) error {
	if n.Err != nil {
		return fmt.Errorf("noop activator error: %w", n.Err)
	}
	return nil
}
