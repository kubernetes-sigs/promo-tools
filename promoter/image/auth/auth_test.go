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
	"errors"
	"testing"
)

func TestStaticIdentityTokenProvider(t *testing.T) {
	p := &StaticIdentityTokenProvider{Token: "oidc-token"}

	tok, err := p.GetIdentityToken(context.Background(), "sa@proj.iam", "sigstore")
	if err != nil {
		t.Fatalf("GetIdentityToken() error = %v", err)
	}

	if tok != "oidc-token" {
		t.Errorf("GetIdentityToken() = %q, want %q", tok, "oidc-token")
	}
}

func TestStaticIdentityTokenProviderError(t *testing.T) {
	p := &StaticIdentityTokenProvider{Err: errors.New("expired")}

	_, err := p.GetIdentityToken(context.Background(), "sa", "aud")
	if err == nil {
		t.Fatal("expected error")
	}
}

// Verify interface compliance at compile time.
var _ IdentityTokenProvider = &StaticIdentityTokenProvider{}
