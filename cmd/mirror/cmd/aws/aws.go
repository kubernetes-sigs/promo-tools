/*
Copyright 2022 The Kubernetes Authors.

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

package aws

import (
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/spf13/cobra"

	"sigs.k8s.io/promo-tools/v3/mirror/pkg/config"
	"sigs.k8s.io/promo-tools/v3/mirror/pkg/image"
	"sigs.k8s.io/promo-tools/v3/mirror/pkg/manager"
	"sigs.k8s.io/promo-tools/v3/mirror/pkg/types"
)

// Cmd represents the `mirror aws` command
var Cmd = &cobra.Command{
	Short: "aws → mirror image layers to AWS regions",
	Long: `aws → mirror image layers to AWS regions",

This subcommand mirrors one or more image layers to a set of AWS regions.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(args)
	},
}

type options struct {
	configPath   string
	bucketPrefix string
}

var opts = options{}

func init() {
	Cmd.PersistentFlags().StringVar(
		&opts.configPath,
		"config-path",
		"",
		"Path to a YAML file containing configuration options for the `mirror aws` subcommand",
	)
	Cmd.PersistentFlags().StringVar(
		&opts.bucketPrefix,
		"bucket-prefix",
		"",
		"A string prefix for the names of S3 Buckets that will be written to. Useful for doing dry-run testing.",
	)
}

func run(imageURIs []string) error {
	var cfg *config.Config
	var err error
	if opts.configPath != "" {
		if cfg, err = config.FromFile(opts.configPath); err != nil {
			return err
		}
	} else {
		cfg = config.New()
	}
	// CLI options override any configuration from file...
	if opts.bucketPrefix != "" {
		cfg.AWS.BucketPrefix = opts.bucketPrefix
	}

	images := make(map[string]ggcrv1.Image, len(imageURIs))
	for _, imageURI := range imageURIs {
		img, err := image.FromRef(imageURI)
		if err != nil {
			return err
		}
		images[imageURI] = img
	}
	var m types.MirrorManager
	m, err = manager.New(cfg)
	if err != nil {
		return err
	}
	for imageURI, image := range images {
		err = m.SyncImage(imageURI, image)
		if err != nil {
			// NOTE(jaypipes): Instead of returning error, should we try to
			// continue mirroring the remaining images?
			return err
		}
	}
	return nil
}
