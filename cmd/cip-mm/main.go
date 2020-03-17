/*
Copyright 2020 The Kubernetes Authors.

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
	reg "sigs.k8s.io/k8s-container-image-promoter/lib/dockerregistry"
	"sigs.k8s.io/yaml"
)

func main() {
	ctx := context.Background()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		// nolint[gomnd]
		os.Exit(1)
	}
}

// nolint[lll]
func run(ctx context.Context) error {
	klog.InitFlags(nil)

	baseDir := ""
	flag.StringVar(
		&baseDir,
		"base_dir",
		baseDir,
		"the manifest directory to look at and modify")
	subprojectDir := ""
	flag.StringVar(
		&subprojectDir,
		"subproject_dir",
		subprojectDir,
		"the directory <name> under <BASE_DIR>/images/<name>, which we want to modify")

	flag.Parse()

	if baseDir == "" {
		return xerrors.New("must specify --base_dir")
	}

	var opt reg.GrowManifestOptions
	opt.PopulateDefaults()

	s, err := filepath.Abs(baseDir)
	if err != nil {
		return xerrors.Errorf("cannot resolve %q to absolute path: %w", baseDir, err)
	}
	opt.BaseDir = s

	manifest, err := reg.GrowManifest(ctx, opt)
	if err != nil {
		return err
	}

	manifestYAML, err := yaml.Marshal(manifest)
	if err != nil {
		return xerrors.Errorf("error serializing manifest: %w", err)
	}

	if _, err := os.Stdout.Write(manifestYAML); err != nil {
		return err
	}

	return nil
}
