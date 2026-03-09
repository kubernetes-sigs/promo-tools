/*
Copyright 2019 The Kubernetes Authors.

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

package run

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"sigs.k8s.io/promo-tools/v4/promobot"
)

// filesCmd represents the subcommand for `kpromo run files`.
var filesCmd = &cobra.Command{
	Use:           "files",
	Short:         "Promote files from a staging object store to production",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := filesOpts.Validate(); err != nil {
			return fmt.Errorf("validating options: %w", err)
		}

		if err := runFilePromotion(filesOpts); err != nil {
			return fmt.Errorf("run `kpromo run files`: %w", err)
		}

		return nil
	},
}

var filesOpts = &promobot.PromoteFilesOptions{}

func init() {
	filesOpts.PopulateDefaults()

	filesCmd.PersistentFlags().StringVar(
		&filesOpts.FilestoresPath,
		"filestores",
		filesOpts.FilestoresPath,
		"path to the `filestores` promoter manifest",
	)

	filesCmd.PersistentFlags().StringVar(
		&filesOpts.FilesPath,
		"files",
		filesOpts.FilesPath,
		"path to the `files` manifest",
	)

	filesCmd.PersistentFlags().StringVar(
		&filesOpts.ManifestsPath,
		"manifests",
		filesOpts.ManifestsPath,
		"path to manifests for multiple projects",
	)

	filesCmd.PersistentFlags().BoolVar(
		&filesOpts.Confirm,
		"confirm",
		filesOpts.Confirm,
		"initiate a PRODUCTION artifact promotion",
	)

	filesCmd.PersistentFlags().BoolVar(
		&filesOpts.UseServiceAccount,
		"use-service-account",
		filesOpts.UseServiceAccount,
		"allow service account usage with gcloud and S3 calls",
	)

	RunCmd.AddCommand(filesCmd)
}

func runFilePromotion(opts *promobot.PromoteFilesOptions) error {
	ctx := context.Background()

	if err := promobot.RunPromoteFiles(ctx, *opts); err != nil {
		return fmt.Errorf("promoting files: %w", err)
	}

	return nil
}
