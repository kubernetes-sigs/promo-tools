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

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	yaml "gopkg.in/yaml.v2"
	"sigs.k8s.io/release-utils/command"

	reg "sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry/registry"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry/schema"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/gcloud"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/stream"
	"sigs.k8s.io/promo-tools/v4/internal/version"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

const kpromoMain = "cmd/kpromo/main.go"

func main() {
	// NOTE: We can't run the tests in parallel because we only have 1 pair of
	// staging/prod GCRs.

	testsPtr := flag.String(
		"tests", "", "the e2e tests file (YAML) to load (REQUIRED)")
	repoRootPtr := flag.String(
		"repo-root", "", "the absolute path of the CIP git repository on disk")
	keyFilePtr := flag.String(
		"key-file", "", "the .json key file to use to activate the service account in the tests (tests only support using 1 service account)")
	helpPtr := flag.Bool(
		"help",
		false,
		"print help")

	flag.Parse()

	// Log linker flags
	printVersion()

	if len(os.Args) == 1 {
		printUsage()
		os.Exit(0)
	}

	if *helpPtr {
		printUsage()
		os.Exit(0)
	}

	if *repoRootPtr == "" {
		logrus.Fatalf("-repo-root=... flag is required")
	}

	ts, err := readE2ETests(*testsPtr)
	if err != nil {
		logrus.Fatal(err)
	}

	if *keyFilePtr != "" {
		if err := gcloud.ActivateServiceAccount(*keyFilePtr); err != nil {
			logrus.Fatalf("activating service account from .json: %q", err)
		}
	}

	// Loop through each e2e test case.
	for _, t := range ts {
		fmt.Printf("\n===> Running e2e test '%s'...\n", t.Name)
		if err := testSetup(*repoRootPtr, &t); err != nil {
			logrus.Fatalf("error with test setup: %q", err)
		}

		fmt.Println("checking snapshots BEFORE promotion:")
		for _, snapshot := range t.Snapshots {
			if err := checkSnapshot(snapshot.Name, snapshot.Before, *repoRootPtr, t.Registries); err != nil {
				logrus.Fatalf("error checking snapshot before promotion for %s: %q", snapshot.Name, err)
			}
		}

		if err = runPromotion(*repoRootPtr, &t); err != nil {
			logrus.Fatalf("error with promotion: %q", err)
		}

		fmt.Println("checking snapshots AFTER promotion:")
		for _, snapshot := range t.Snapshots {
			if err := checkSnapshot(snapshot.Name, snapshot.After, *repoRootPtr, t.Registries); err != nil {
				logrus.Fatalf("error checking snapshot for %s: %q", snapshot.Name, err)
			}
		}

		fmt.Printf("\n===> e2e test '%s': OK\n", t.Name)
	}
}

// removeSignatureLayers removes the signature layers from a snapshot.
func removeSignatureLayers(snapshot *[]registry.Image) {
	var remove []image.Digest
	for i := range *snapshot {
		remove = []image.Digest{}
		for dgst, tags := range (*snapshot)[i].Dmap {
			if len(tags) == 0 || // Recursive signing may add additional layers without tags
				(len(tags) == 1 && strings.HasSuffix(string(tags[0]), ".sig")) { // Signature layers only have one tag
				remove = append(remove, dgst)
			}
		}
		for _, dgst := range remove {
			delete((*snapshot)[i].Dmap, dgst)
		}
	}
}

func checkSnapshot(
	repo image.Registry,
	expected []registry.Image,
	repoRoot string,
	rcs []registry.Context,
) error {
	// Get the snapshot of the repo
	got, err := getSnapshot(repoRoot, repo, rcs)
	if err != nil {
		return fmt.Errorf("getting snapshot of %s: %w", repo, err)
	}

	// After signing images, the repo snapshots will never match. In order
	// to compare them, we remove the signature layers from the current
	// snapshot to ensure the original images were promoted.
	removeSignatureLayers(&got)
	removeSignatureLayers(&expected)

	diff := cmp.Diff(got, expected)
	if diff != "" {
		fmt.Printf("the following diff exists: %s", diff)
		return errors.New("expected equivalent image sets")
	}

	return nil
}

func testSetup(repoRoot string, t *E2ETest) error {
	if err := t.clearRepositories(); err != nil {
		return fmt.Errorf("cleaning test repository: %w", err)
	}

	goldenPush := repoRoot + "/test-e2e/golden-images/push-golden.sh"

	cmd := command.NewWithWorkDir(
		repoRoot,
		goldenPush,
	)

	logrus.Infof("executing %s\n", cmd.String())

	std, err := cmd.RunSuccessOutput()
	fmt.Println(std.Output())
	fmt.Println(std.Error())
	return err
}

func runPromotion(repoRoot string, t *E2ETest) error {
	args := []string{
		"run",
		fmt.Sprintf(
			"%s/%s",
			repoRoot,
			kpromoMain,
		),
		"cip",
		"--confirm",
		"--log-level=debug",
		"--use-service-account",
		// There is no need to use -key-files=... because we already activated
		// the 1 service account we need during e2e tests with our own -key-file
		// flag.
		"--certificate-identity=k8s-infra-promoter-test-signer@k8s-cip-test-prod.iam.gserviceaccount.com",
		"--certificate-oidc-issuer=https://accounts.google.com",
	}

	argsFinal := []string{}

	args = append(args, t.Invocation...)

	for _, arg := range args {
		argsFinal = append(argsFinal, strings.ReplaceAll(arg, "$PWD", repoRoot))
	}

	fmt.Println("execing cmd", "go", argsFinal)
	cmd := command.NewWithWorkDir(repoRoot, "go", argsFinal...)
	return cmd.RunSuccess()
}

func extractSvcAcc(r image.Registry, rcs []registry.Context) string {
	for _, regCtx := range rcs {
		if regCtx.Name == r {
			return regCtx.ServiceAccount
		}
	}

	return ""
}

func getSnapshot(
	repoRoot string,
	registryName image.Registry,
	rcs []registry.Context,
) ([]registry.Image, error) {
	// TODO: Consider setting flag names in `cip` instead
	invocation := []string{
		"run",
		fmt.Sprintf(
			"%s/%s",
			repoRoot,
			kpromoMain,
		),
		"cip",
		"--snapshot=" + string(registryName),
	}

	svcAcc := extractSvcAcc(registryName, rcs)
	if svcAcc != "" {
		invocation = append(invocation, "--snapshot-service-account="+svcAcc)
	}

	fmt.Println("execing cmd", "go", invocation)
	// TODO: Replace with sigs.k8s.io/release-utils/command once the package
	//       exposes a means to manipulate stdout.Bytes() for unmarshalling.
	cmd := exec.Command("go", invocation...)

	cmd.Dir = repoRoot

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		fmt.Println("stdout", stdout.String())
		fmt.Println("stderr", stderr.String())
		return nil, err
	}

	images := make(registry.Images, 0)
	if err := yaml.UnmarshalStrict(stdout.Bytes(), &images); err != nil {
		return nil, err
	}

	return images, err
}

func clearRepository(regName image.Registry, sc *reg.SyncContext) {
	mkDeletionCmd := func(
		dest registry.Context,
		imageName image.Name,
		digest image.Digest,
	) stream.Producer {
		var sp stream.Subprocess
		sp.CmdInvocation = reg.GetDeleteCmd(
			dest,
			sc.UseServiceAccount,
			imageName,
			digest,
			true)
		return &sp
	}

	sc.ClearRepository(regName, mkDeletionCmd, nil)
}

// E2ETest holds all the information about a single e2e test. It has the
// promoter manifest, and the before/after snapshots of all repositories that it
// cares about.
type E2ETest struct {
	Name       string             `yaml:"name,omitempty"`
	Registries []registry.Context `yaml:"registries,omitempty"`
	Invocation []string           `yaml:"invocation,omitempty"`
	Snapshots  []RegistrySnapshot `yaml:"snapshots,omitempty"`
}

// E2ETests is an array of E2ETest.
type E2ETests []E2ETest

// RegistrySnapshot is the snapshot of a registry. It is basically the key/value
// pair out of the reg.MasterInventory type (RegistryName + []Image).
type RegistrySnapshot struct {
	Name   image.Registry  `yaml:"name,omitempty"`
	Before registry.Images `yaml:"before,omitempty"`
	After  registry.Images `yaml:"after,omitempty"`
}

func (t *E2ETest) clearRepositories() error {
	// We need a SyncContext to clear the repos. That's it. The actual
	// promotions will be done by the cip binary, not this tool.
	sc, err := reg.MakeSyncContext(
		[]schema.Manifest{
			{Registries: t.Registries},
		},
		10,
		true,
		true)
	if err != nil {
		return fmt.Errorf("trying to create sync context: %w", err)
	}

	sc.ReadRegistries(
		sc.RegistryContexts,
		// Read all registries recursively, because we want to delete every
		// image found in it (clearRepository works by deleting each image found
		// in sc.Inv).
		true,
		reg.MkReadRepositoryCmdReal)

	// Clear ALL registries in the test manifest. Blank slate!
	for _, rc := range t.Registries {
		fmt.Println("CLEARING REPO", rc.Name)
		clearRepository(rc.Name, sc)
	}
	return nil
}

func readE2ETests(filePath string) (E2ETests, error) {
	var ts E2ETests
	b, err := os.ReadFile(filePath)
	if err != nil {
		return ts, err
	}
	if err := yaml.UnmarshalStrict(b, &ts); err != nil {
		return ts, err
	}

	return ts, nil
}

func printVersion() {
	logrus.Infof("%s", version.Get().String())
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	flag.PrintDefaults()
}
