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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/xerrors"
	"k8s.io/klog"
	api "sigs.k8s.io/k8s-container-image-promoter/pkg/api/files"
	"sigs.k8s.io/k8s-container-image-promoter/pkg/cmd"
	"sigs.k8s.io/k8s-container-image-promoter/pkg/gcloud"
	"sigs.k8s.io/yaml"
)

func main() {
	ctx := context.Background()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		// nolint[gomnd]
		os.Exit(1)
	}
	os.Exit(0)
}

func run(ctx context.Context) error {
	klog.InitFlags(nil)

	var promoteOptions cmd.PromoteFilesOptions
	promoteOptions.PopulateDefaults()

	src := ""
	flag.StringVar(
		&src,
		"src",
		src,
		"the base directory to copy from")

	dest := ""
	flag.StringVar(
		&dest,
		"dest",
		dest,
		"the location to copy to")

	flag.BoolVar(
		&promoteOptions.UseServiceAccount,
		"use-service-account",
		promoteOptions.UseServiceAccount,
		"allow service account usage with gcloud calls"+
			" (default: false)")

	flag.BoolVar(
		&promoteOptions.DryRun,
		"dry-run",
		promoteOptions.DryRun,
		"print what would have happened by running this tool;"+
			" do not actually modify any registry")

	flag.Parse()

	if src == "" {
		return xerrors.New("must specify --src")
	}
	if dest == "" {
		return xerrors.New("must specify --dest")
	}

	if s, err := filepath.Abs(src); err != nil {
		return xerrors.Errorf("cannot resolve %q to absolute path: %w", src, err)
	} else {
		src = s
	}

	serviceAccount := ""
	if promoteOptions.UseServiceAccount {
		s, err := gcloud.CurrentAccount()
		if err != nil {
			return err
		}
		serviceAccount = s
	}

	var hashOptions cmd.HashFilesOptions
	hashOptions.BaseDir = src

	manifest, err := cmd.RunHashFiles(ctx, hashOptions)
	if err != nil {
		return err
	}

	// Serialize partial manifest before we add to it
	manifestYAML, err := yaml.Marshal(manifest)
	if err != nil {
		return xerrors.Errorf("error serializing manifest: %w", err)
	}

	manifest.Filestores = append(manifest.Filestores, api.Filestore{
		Base: "file://" + src,
		Src:  true,
	})

	manifest.Filestores = append(manifest.Filestores, api.Filestore{
		Base:           dest,
		Src:            false,
		ServiceAccount: serviceAccount,
	})

	promoteOptions.Manifest = manifest

	if err := cmd.RunPromoteFiles(ctx, promoteOptions); err != nil {
		return err
	}

	// Print manifest to stdout
	if _, err := os.Stdout.Write(manifestYAML); err != nil {
		return err
	}

	return nil
}
