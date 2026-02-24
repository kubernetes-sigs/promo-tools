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

package cip

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	promoter "sigs.k8s.io/promo-tools/v4/promoter/image"
)

var replicateCmd = &cobra.Command{
	Use:   "replicate-signatures",
	Short: "Replicate image signatures to mirror registries",
	Long: `replicate-signatures - Replicate image signatures to mirror registries

This subcommand reads the promotion manifests to discover which images
should exist in which registries, then copies any missing signature tags
from the primary registry to the mirrors.

The operation is fully idempotent; re-running when everything is already
replicated is a series of fast existence checks.

Example usage:

  kpromo cip replicate-signatures \
    --thin-manifest-dir=/path/to/manifests \
    --confirm
`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		p := promoter.New(runOpts)
		if err := p.ReplicateSignatures(context.Background(), runOpts); err != nil {
			return fmt.Errorf("replicate signatures: %w", err)
		}
		return nil
	},
}

func init() {
	CipCmd.AddCommand(replicateCmd)
}
