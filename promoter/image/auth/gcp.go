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
	"strings"

	credentials "cloud.google.com/go/iam/credentials/apiv1"
	"cloud.google.com/go/iam/credentials/apiv1/credentialspb"
	"github.com/sirupsen/logrus"
)

// GCPIdentityTokenProvider implements IdentityTokenProvider using the
// GCP IAM Credentials API with Application Default Credentials.
type GCPIdentityTokenProvider struct{}

// GetIdentityToken generates an OIDC identity token for the given service
// account using the GCP IAM Credentials API.
func (g *GCPIdentityTokenProvider) GetIdentityToken(
	ctx context.Context, serviceAccount, audience string,
) (string, error) {
	c, err := credentials.NewIamCredentialsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("creating IAM credentials client: %w", err)
	}
	defer c.Close()

	logrus.Infof("Signing identity for images will be %s", serviceAccount)

	name := serviceAccount
	if !strings.HasPrefix(name, "projects/") {
		name = "projects/-/serviceAccounts/" + serviceAccount
	}

	resp, err := c.GenerateIdToken(ctx, &credentialspb.GenerateIdTokenRequest{
		Name:         name,
		Audience:     audience,
		IncludeEmail: true,
	})
	if err != nil {
		return "", fmt.Errorf("generating identity token: %w", err)
	}

	return resp.GetToken(), nil
}
