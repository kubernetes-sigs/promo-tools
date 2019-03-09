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

	// nolint[lll]
	reg "github.com/kubernetes-sigs/k8s-container-image-promoter/lib/dockerregistry"
	"github.com/kubernetes-sigs/k8s-container-image-promoter/lib/stream"
)

func main() {
	manifestPtr := flag.String(
		"manifest", "manifest.yaml", "the manifest file to load")
	garbageCollectPtr := flag.Bool(
		"garbage-collect",
		false, "delete all untagged images in the destination registry")
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
	deleteExtraTags := flag.Bool(
		"delete-extra-tags",
		false,
		"delete tags in the destination registry that are not declared"+
			" in the Manifest (default: false)")
	dryRunPtr := flag.Bool(
		"dry-run",
		false,
		"print what would have happened by running this tool;"+
			" do not actually modify any registry (default: false)")
	flag.Parse()

	if *dryRunPtr {
		fmt.Println("---------- DRY RUN ----------")
	}

	mfest := reg.ParseManifestFromFile(*manifestPtr)
	sc := reg.MakeSyncContext(map[reg.RegistryName]reg.RegInvImage{
		mfest.Registries.Src:  nil,
		mfest.Registries.Dest: nil},
		*verbosityPtr, *threadsPtr, *deleteExtraTags, *dryRunPtr)

	// Read the state of the world; i.e., populate the SyncContext.
	mkRegistryListingCmd := func(regName reg.RegistryName) stream.Producer {
		var sp stream.Subprocess
		sp.CmdInvocation = reg.GetRegistryListingCmd(
			mfest.ServiceAccount,
			string(regName))
		return &sp
	}
	sc.ReadImageNames(mkRegistryListingCmd)
	mkRegistryListTagsCmd := func(
		registryName reg.RegistryName, imgName reg.ImageName) stream.Producer {
		var sp stream.Subprocess
		sp.CmdInvocation = reg.GetRegistryListTagsCmd(
			mfest.ServiceAccount,
			string(registryName),
			string(imgName))
		return &sp
	}
	sc.ReadDigestsAndTags(mkRegistryListTagsCmd)

	sc.Info(sc.Inv.PrettyValue())

	// Promote.
	mkPromotionCmd := func(
		srcRegistry,
		destRegistry reg.RegistryName,
		imageName reg.ImageName,
		digest reg.Digest, tag reg.Tag, tp reg.TagOp) stream.Producer {
		var sp stream.Subprocess
		sp.CmdInvocation = reg.GetWriteCmd(
			mfest.ServiceAccount,
			srcRegistry,
			destRegistry,
			imageName,
			digest,
			tag,
			tp)
		return &sp
	}
	sc.Promote(mfest, mkPromotionCmd, nil)

	if *garbageCollectPtr {
		sc.Info("---------- BEGIN GARBAGE COLLECTION ----------")
		// Re-read the state of the world.
		sc.ReadImageNames(mkRegistryListingCmd)
		sc.ReadDigestsAndTags(mkRegistryListTagsCmd)
		// Garbage-collect all untagged images in dest registry.
		mkTagDeletionCmd := func(
			destRegistry reg.RegistryName,
			imageName reg.ImageName,
			digest reg.Digest) stream.Producer {
			var sp stream.Subprocess
			sp.CmdInvocation = reg.GetDeleteCmd(
				mfest.ServiceAccount,
				destRegistry,
				imageName,
				digest)
			return &sp
		}
		sc.GarbageCollect(mfest, mkTagDeletionCmd, nil)
	}
}
