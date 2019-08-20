/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"k8s.io/klog"
	api "sigs.k8s.io/k8s-container-image-promoter/pkg/api/files"
	"sigs.k8s.io/k8s-container-image-promoter/pkg/filepromoter"
)

// PromoteFilesOptions holds the flag-values for a file promotion
type PromoteFilesOptions struct {
	// ManifestPath is the path to the manifest file to run
	ManifestPath string

	// DryRun (if set) will not perform operations, but print them instead
	DryRun bool

	// UseServiceAccount must be true, for service accounts to be used
	// This gives some protection against a hostile manifest.
	UseServiceAccount bool

	// Out is the destination for "normal" output (such as dry-run)
	Out io.Writer
}

// PopulateDefaults sets the default values for PromoteFilesOptions
func (o *PromoteFilesOptions) PopulateDefaults() {
	o.DryRun = true
	o.UseServiceAccount = false
	o.Out = os.Stdout
}

// RunPromoteFiles executes a file promotion command
// nolint[gocyclo]
func RunPromoteFiles(ctx context.Context, options PromoteFilesOptions) error {
	manifest, err := readManifest(options.ManifestPath)
	if err != nil {
		return err
	}

	if options.DryRun {
		fmt.Fprintf(
			options.Out,
			"********** START (DRY RUN): %s **********\n",
			options.ManifestPath)
	} else {
		fmt.Fprintf(
			options.Out,
			"********** START: %s **********\n",
			options.ManifestPath)
	}

	promoter := &filepromoter.ManifestPromoter{
		Manifest:          manifest,
		UseServiceAccount: options.UseServiceAccount,
	}

	ops, err := promoter.BuildOperations(ctx)
	if err != nil {
		return fmt.Errorf(
			"error building operations for %q: %v",
			options.ManifestPath, err)
	}

	// So that we can support future parallel execution, an error
	// in one operation does not prevent us attempting the
	// remaining operations
	var errors []error
	for _, op := range ops {
		if _, err := fmt.Fprintf(options.Out, "%v\n", op); err != nil {
			errors = append(errors, fmt.Errorf(
				"error writing to output: %v", err))
		}

		if !options.DryRun {
			if err := op.Run(ctx); err != nil {
				klog.Warningf("error copying file: %v", err)
				errors = append(errors, err)
			}
		}
	}

	if len(errors) != 0 {
		fmt.Fprintf(
			options.Out,
			"********** FINISHED WITH ERRORS: %s **********\n",
			options.ManifestPath)
		for _, err := range errors {
			fmt.Fprintf(options.Out, "%v\n", err)
		}

		return errors[0]
	}

	if options.DryRun {
		fmt.Fprintf(
			options.Out,
			"********** FINISHED (DRY RUN): %s **********\n",
			options.ManifestPath)
	} else {
		fmt.Fprintf(
			options.Out,
			"********** FINISHED: %s **********\n",
			options.ManifestPath)
	}

	return nil
}

func readManifest(p string) (*api.Manifest, error) {
	if p == "" {
		return nil, fmt.Errorf("-manifest=... is required")
	}

	b, err := ioutil.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("error reading manifest %q: %v", p, err)
	}

	manifest, err := api.ParseManifest(b)
	if err != nil {
		return nil, fmt.Errorf("error parsing manifest %q: %v", p, err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("error validating manifest %q: %v", p, err)
	}

	return manifest, nil
}
