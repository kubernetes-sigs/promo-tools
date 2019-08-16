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
	"flag"
	"fmt"
	"os"

	// nolint[lll]
	"k8s.io/klog"
	reg "sigs.k8s.io/k8s-container-image-promoter/lib/dockerregistry"
	"sigs.k8s.io/k8s-container-image-promoter/lib/stream"
	"sigs.k8s.io/k8s-container-image-promoter/pkg/gcloud"
)

// GitDescribe is stamped by bazel.
var GitDescribe string

// GitCommit is stamped by bazel.
var GitCommit string

// TimestampUtcRfc3339 is stamped by bazel.
var TimestampUtcRfc3339 string

// nolint[gocyclo]
func main() {
	klog.InitFlags(nil)

	manifestPtr := flag.String(
		"manifest", "", "the manifest file to load (REQUIRED)")
	manifestDirPtr := flag.String(
		"manifest-dir",
		"",
		"recursively read in all manifests within a folder; it is an error if two manifests specify conflicting intent (e.g., promotion of the same image)")
	threadsPtr := flag.Int(
		"threads",
		10, "number of concurrent goroutines to use when talking to GCR")
	verbosityPtr := flag.Int(
		"verbosity",
		2,
		"verbosity level for logging;"+
			" 0 = fatal only,"+
			" 1 = fatal + errors,"+
			" 2 = fatal + errors + warnings,"+
			" 3 = fatal + errors + warnings + informational (everything)")
	parseOnlyPtr := flag.Bool(
		"parse-only",
		false,
		"only check that the given manifest file is parseable as a Manifest"+
			" (default: false)")
	dryRunPtr := flag.Bool(
		"dry-run",
		true,
		"print what would have happened by running this tool;"+
			" do not actually modify any registry")
	keyFilesPtr := flag.String(
		"key-files",
		"",
		"CSV of service account key files that must be activated for the promotion (<json-key-file-path>,...)")
	// Add in help flag information, because Go's "flag" package automatically
	// adds it, but for whatever reason does not show it as part of available
	// options.
	helpPtr := flag.Bool(
		"help",
		false,
		"print help")
	versionPtr := flag.Bool(
		"version",
		false,
		"print version")
	snapshotPtr := flag.String(
		"snapshot",
		"",
		"read all images in a repository and print to stdout")
	snapshotTag := ""
	flag.StringVar(&snapshotTag, "snapshot-tag", snapshotTag, "only snapshot images with the given tag")
	snapshotSvcAccPtr := flag.String(
		"snapshot-service-account",
		"",
		"service account to use for -snapshot")
	noSvcAcc := false
	flag.BoolVar(&noSvcAcc, "no-service-account", false,
		"do not pass '--account=...' to all gcloud calls (default: false)")
	flag.Parse()

	if len(os.Args) == 1 {
		printVersion()
		printUsage()
		os.Exit(0)
	}

	if *helpPtr {
		printUsage()
		os.Exit(0)
	}

	if *versionPtr {
		printVersion()
		os.Exit(0)
	}

	// Activate service accounts.
	if len(*keyFilesPtr) > 0 {
		if err := gcloud.ActivateServiceAccounts(*keyFilesPtr); err != nil {
			klog.Exitln(err)
		}
	}

	var mfest reg.Manifest
	var srcRegistry *reg.RegistryContext
	var err error
	var mfests []reg.Manifest
	sc := reg.SyncContext{}
	mi := make(reg.MasterInventory)

	if len(*snapshotPtr) > 0 {
		srcRegistry = &reg.RegistryContext{
			Name:           reg.RegistryName(*snapshotPtr),
			ServiceAccount: *snapshotSvcAccPtr,
			Src:            true,
		}
		mfests = []reg.Manifest{
			{
				Registries: []reg.RegistryContext{
					*srcRegistry,
				},
				Images: []reg.Image{},
			},
		}
	} else {
		if *manifestPtr == "" && *manifestDirPtr == "" {
			klog.Fatal(fmt.Errorf("-manifest=... or -manifestDir=... flag is required"))
		}
	}

	if *manifestPtr != "" {
		mfest, err = reg.ParseManifestFromFile(*manifestPtr)
		if err != nil {
			klog.Fatal(err)
		}
		mfests = append(mfests, mfest)
		for _, registry := range mfest.Registries {
			mi[registry.Name] = nil
		}
		sc, err = reg.MakeSyncContext(
			mfests,
			*verbosityPtr,
			*threadsPtr,
			*dryRunPtr,
			!noSvcAcc)
		if err != nil {
			klog.Fatal(err)
		}
	} else if *manifestDirPtr != "" {
		mfests, err = reg.ParseManifestsFromDir(*manifestDirPtr)
		if err != nil {
			klog.Exitln(err)
		}

		err = reg.ValidateManifestsFromDir(mfests)
		if err != nil {
			klog.Exitln(err)
		}

		sc, err = reg.MakeSyncContext(
			mfests,
			*verbosityPtr,
			*threadsPtr,
			*dryRunPtr,
			!noSvcAcc)
		if err != nil {
			klog.Fatal(err)
		}
	}

	if *parseOnlyPtr {
		os.Exit(0)
	}

	// If there are no images in the manifest, it may be a stub manifest file
	// (such as for brand new registries that would be watched by the promoter
	// for the very first time). In any case, we do NOT want to process such
	// manifests, because other logic like garbage collection would think that
	// the manifest desires a completely blank registry. In practice this would
	// almost never be the case, so given a fully-parsed manifest with 0 images,
	// treat it as if -parse-only was implied and exit gracefully.
	if len(*snapshotPtr) == 0 {
		imagesInManifests := false
		for _, mfest := range mfests {
			if len(mfest.Images) > 0 {
				imagesInManifests = true
				break
			}
		}
		if !imagesInManifests {
			fmt.Println("No images in manifest(s) --- nothing to do.")
			os.Exit(0)
		}

		if *dryRunPtr {
			fmt.Printf("********** START (DRY RUN): %s **********\n", *manifestPtr)
		} else {
			fmt.Printf("********** START: %s **********\n", *manifestPtr)
		}
	}

	if len(*snapshotPtr) > 0 {
		sc, err = reg.MakeSyncContext(
			mfests,
			*verbosityPtr,
			*threadsPtr,
			*dryRunPtr,
			!noSvcAcc)
		if err != nil {
			klog.Fatal(err)
		}
		sc.ReadRegistries(
			[]reg.RegistryContext{*srcRegistry},
			// Read all registries recursively, because we want to produce a
			// complete snapshot.
			true,
			reg.MkReadRepositoryCmdReal)

		rii := sc.Inv[mfests[0].Registries[0].Name]
		if snapshotTag != "" {
			filtered := make(reg.RegInvImage)
			for imageName, digestTags := range rii {
				for digest, tags := range digestTags {
					for _, tag := range tags {
						if string(tag) == snapshotTag {
							if filtered[imageName] == nil {
								filtered[imageName] = make(reg.DigestTags)
							}
							filtered[imageName][digest] = append(filtered[imageName][digest], tag)
						}
					}
				}
			}
			rii = filtered
		}

		snapshot := rii.ToYAML()
		fmt.Print(snapshot)
		os.Exit(0)
	}

	// Promote.
	edges := sc.GetPromotionEdgesFromManifests(mfests, true)
	mkProducer := func(
		srcRegistry reg.RegistryName,
		srcImageName reg.ImageName,
		destRC reg.RegistryContext,
		imageName reg.ImageName,
		digest reg.Digest, tag reg.Tag, tp reg.TagOp) stream.Producer {
		var sp stream.Subprocess
		sp.CmdInvocation = reg.GetWriteCmd(
			destRC,
			sc.UseServiceAccount,
			srcRegistry,
			srcImageName,
			imageName,
			digest,
			tag,
			tp)
		return &sp
	}
	err = sc.Promote(edges, mkProducer, nil)
	if err != nil {
		klog.Exitln(err)
	}

	if *dryRunPtr {
		fmt.Printf("********** FINISHED (DRY RUN): %s **********\n",
			*manifestPtr)
	} else {
		fmt.Printf("********** FINISHED: %s **********\n", *manifestPtr)
	}
}

func printVersion() {
	fmt.Printf("Built:   %s\n", TimestampUtcRfc3339)
	fmt.Printf("Version: %s\n", GitDescribe)
	fmt.Printf("Commit:  %s\n", GitCommit)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	flag.PrintDefaults()
}
