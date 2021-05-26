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

package cmd

import (
	"github.com/spf13/cobra"

	"sigs.k8s.io/k8s-container-image-promoter/legacy/cli"
)

var versionOpts = &cli.VersionOptions{}

// versionCmd is the command when calling `cip version`.
var versionCmd = &cobra.Command{
	Use:           "version",
	Short:         "output version information",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.RunVersionCmd(versionOpts)
	},
}

func init() {
	versionCmd.PersistentFlags().BoolVarP(
		&versionOpts.JSON,
		"json",
		"j",
		false,
		"print JSON instead of text",
	)

	rootCmd.AddCommand(versionCmd)
}
