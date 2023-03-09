/*
Copyright 2023 The Kubernetes Authors.

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

package sigcheck

import (
	"github.com/spf13/cobra"
	imagepromoter "sigs.k8s.io/promo-tools/v3/promoter/image"
	promoteropts "sigs.k8s.io/promo-tools/v3/promoter/image/options"
)

func Add(parent *cobra.Command) {
	opts := &promoteropts.Options{}
	cmd := &cobra.Command{
		Use:   "sigcheck",
		Short: "Check image signature consistency",
		Long: `sigcheck - Check signature consistency across the K8s mirrors
    
This subcommand checks the signature consistency across promoted images
to ensure copies in all mirrors have their signatures attached.
    `,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.SignCheckReferences = args
			}

			p := imagepromoter.New()
			if err := p.CheckSignatures(opts); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.PersistentFlags().BoolVar(
		&opts.SignCheckFix,
		"confirm",
		false,
		"when true, kpromo will sign and propagate missing signatures in images",
	)

	cmd.PersistentFlags().IntVar(
		&opts.SignCheckFromDays,
		"from-days",
		5,
		"check images uploaded starting this many days ago",
	)

	cmd.PersistentFlags().IntVar(
		&opts.SignCheckToDays,
		"to-days",
		0,
		"check images --from-days ago to this many days ago (defaults to today)",
	)

	parent.AddCommand(cmd)
}
