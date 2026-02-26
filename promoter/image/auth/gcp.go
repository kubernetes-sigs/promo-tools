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
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	credentials "cloud.google.com/go/iam/credentials/apiv1"
	"cloud.google.com/go/iam/credentials/apiv1/credentialspb"
	"github.com/sirupsen/logrus"
	gopts "google.golang.org/api/option"
	"sigs.k8s.io/release-utils/command"
)

// GCPIdentityTokenProvider implements IdentityTokenProvider using the
// GCP IAM Credentials API.
type GCPIdentityTokenProvider struct {
	// CredentialsFile is an optional path to a credentials JSON file.
	// If empty, Application Default Credentials are used.
	CredentialsFile string
}

// GetIdentityToken generates an OIDC identity token for the given service
// account using the GCP IAM Credentials API.
func (g *GCPIdentityTokenProvider) GetIdentityToken(
	ctx context.Context, serviceAccount, audience string,
) (string, error) {
	var clientOpts []gopts.ClientOption

	if g.CredentialsFile != "" {
		logrus.Infof("Using credentials from %s", g.CredentialsFile)
		//nolint:staticcheck // Credentials file is user-provided for service account impersonation.
		clientOpts = append(clientOpts, gopts.WithCredentialsFile(g.CredentialsFile))
	}

	c, err := credentials.NewIamCredentialsClient(ctx, clientOpts...)
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

// GCPServiceActivator implements ServiceActivator using gcloud CLI.
type GCPServiceActivator struct{}

// ActivateServiceAccounts activates service accounts via gcloud.
func (g *GCPServiceActivator) ActivateServiceAccounts(_ context.Context, keyFilePaths string) error {
	r := csv.NewReader(strings.NewReader(keyFilePaths))
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return fmt.Errorf("reading key file paths: %w", err)
		}

		for _, keyFilePath := range record {
			cmd := command.New(
				"gcloud",
				"auth",
				"activate-service-account",
				"--key-file="+keyFilePath,
			)
			if err := cmd.RunSuccess(); err != nil {
				return fmt.Errorf("activating service account from %s: %w", keyFilePath, err)
			}
		}
	}

	return nil
}
