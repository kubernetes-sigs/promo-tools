package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"reflect"
	"strings"

	yaml "gopkg.in/yaml.v2"
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

// nolint[lll]
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

	if len(os.Args) == 1 {
		printVersion()
		printUsage()
		os.Exit(0)
	}

	if *helpPtr {
		printUsage()
		os.Exit(0)
	}

	if *repoRootPtr == "" {
		klog.Fatal(fmt.Errorf("-repo-root=... flag is required"))
	}

	ts, err := readE2ETests(*testsPtr)
	if err != nil {
		klog.Fatal(err)
	}

	if len(*keyFilePtr) > 0 {
		if err := gcloud.ActivateServiceAccount(*keyFilePtr); err != nil {
			klog.Fatal("could not activate service account from .json", err)
		}
	}

	// Loop through each e2e test case.
	for _, t := range ts {
		fmt.Printf("\n===> Running e2e test '%s'...\n", t.Name)
		err := testSetup(*repoRootPtr, t)
		if err != nil {
			klog.Fatal("error with test setup:", err)
		}

		fmt.Println("checking snapshots BEFORE promotion:")
		for _, snapshot := range t.Snapshots {
			checkSnapshot(snapshot.Name, snapshot.Before, *repoRootPtr, t.Registries)
		}

		err = runPromotion(*repoRootPtr, t)
		if err != nil {
			klog.Fatal("error with promotion:", err)
		}

		fmt.Println("checking snapshots AFTER promotion:")
		for _, snapshot := range t.Snapshots {
			checkSnapshot(snapshot.Name, snapshot.After, *repoRootPtr, t.Registries)
		}

		fmt.Printf("\n===> e2e test '%s': OK\n", t.Name)
	}
}

func checkSnapshot(repo reg.RegistryName,
	expected []reg.Image,
	repoRoot string,
	rcs []reg.RegistryContext) {

	got, err := getSnapshot(
		repoRoot,
		repo,
		rcs)
	if err != nil {
		klog.Exitf("could not get snapshot of %s: %s\n", repo, err)
	}
	if err := checkEqual(got, expected); err != nil {
		klog.Exitln(err)
	}
}

// nolint[funlen]
func testSetup(repoRoot string, t E2ETest) error {
	if err := t.clearRepositories(); err != nil {
		return err
	}

	pushRepo := getBazelOption(repoRoot, "STABLE_TEST_STAGING_IMG_REPOSITORY")

	if pushRepo == "" {
		return fmt.Errorf(
			"could not dereference STABLE_TEST_STAGING_IMG_REPOSITORY")
	}

	cmds := [][]string{
		// In order to create a manifest list, images must be pushed to a
		// repository first.
		{
			"bazel",
			"run",
			"--host_force_python=PY2",
			fmt.Sprintf(
				"--workspace_status_command=%s/workspace_status.sh",
				repoRoot),
			"//test-e2e:push-golden",
		},
		{
			"docker",
			"manifest",
			"create",
			fmt.Sprintf("%s/golden-foo/foo:1.0", pushRepo),
			fmt.Sprintf("%s/golden-foo/foo:1.0-linux_amd64", pushRepo),
			fmt.Sprintf("%s/golden-foo/foo:1.0-linux_s390x", pushRepo),
		},
		// Fixup the s390x image because it's set to amd64 by default (there is
		// no way to specify architecture from within bazel yet when creating
		// images).
		{
			"docker",
			"manifest",
			"annotate",
			"--arch=s390x",
			fmt.Sprintf("%s/golden-foo/foo:1.0", pushRepo),
			fmt.Sprintf("%s/golden-foo/foo:1.0-linux_s390x", pushRepo),
		},
		{
			"docker",
			"manifest",
			"inspect",
			fmt.Sprintf("%s/golden-foo/foo:1.0", pushRepo),
		},
		// Finally, push the manifest list. It is just metadata around existing
		// images in a repository.
		{
			"docker",
			"manifest",
			"push",
			"--purge",
			fmt.Sprintf("%s/golden-foo/foo:1.0", pushRepo),
		},
		// Remove tag for tagless image.
		{
			"gcloud",
			"container",
			"images",
			"untag",
			"--quiet",
			fmt.Sprintf("%s/golden-foo/foo:NOTAG-0", pushRepo),
		},
	}

	for _, cmd := range cmds {
		fmt.Println("execing cmd", cmd)
		stdout, stderr, err := execCommand(repoRoot, cmd[0], cmd[1:]...)
		if err != nil {
			return err
		}
		fmt.Println(stdout)
		fmt.Println(stderr)
	}

	return nil
}

func execCommand(
	repoRoot, cmdString string,
	args ...string) (string, string, error) {

	cmd := exec.Command(cmdString, args...)
	if repoRoot != "" {
		cmd.Dir = repoRoot
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		klog.Errorf("for command %s:\nstdout:\n%sstderr:\n%s\n",
			cmdString,
			stdout.String(),
			stderr.String())
		return "", "", err
	}
	return stdout.String(), stderr.String(), nil
}

func getBazelOption(repoRoot, o string) string {
	stdout, _, err := execCommand(repoRoot, "./workspace_status.sh")
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		if strings.Contains(line, o) {
			words := strings.Split(line, " ")
			if len(words) == 2 {
				return words[1]
			}
		}
	}
	return ""
}

func runPromotion(repoRoot string, t E2ETest) error {
	args := []string{
		"run",
		"--workspace_status_command=" + repoRoot + "/workspace_status.sh",
		"--stamp",
		":cip",
		"--",
		"-dry-run=false",
		"-verbosity=3",
		// There is no need to use -key-files=... because we already activated
		// the 1 service account we need during e2e tests with our own -key-file
		// flag.
	}

	argsFinal := []string{}

	args = append(args, t.Invocation...)

	for _, arg := range args {
		argsFinal = append(argsFinal, strings.ReplaceAll(arg, "$PWD", repoRoot))
	}

	fmt.Println("execing cmd", "bazel", argsFinal)
	cmd := exec.Command(
		"bazel",
		argsFinal...,
	)

	cmd.Dir = repoRoot

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func extractSvcAcc(
	registry reg.RegistryName,
	rcs []reg.RegistryContext) string {

	for _, r := range rcs {
		if r.Name == registry {
			return r.ServiceAccount
		}
	}
	return ""
}

func getSnapshot(repoRoot string,
	registry reg.RegistryName,
	rcs []reg.RegistryContext) ([]reg.Image, error) {

	invocation := []string{
		"run",
		"--workspace_status_command=" + repoRoot + "/workspace_status.sh",
		":cip",
		"--",
		"-snapshot=" + string(registry)}

	svcAcc := extractSvcAcc(registry, rcs)
	if len(svcAcc) > 0 {
		invocation = append(invocation, "-snapshot-service-account="+svcAcc)
	}

	fmt.Println("execing cmd", "bazel", invocation)
	cmd := exec.Command("bazel", invocation...)

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

	images := make([]reg.Image, 0)
	if err = yaml.UnmarshalStrict(stdout.Bytes(), &images); err != nil {
		return nil, err
	}
	return images, err
}

func (t *E2ETest) clearRepositories() error {
	// We need a SyncContext to clear the repos. That's it. The actual
	// promotions will be done by the cip binary, not this tool.
	sc, err := reg.MakeSyncContext(
		[]reg.Manifest{
			{Registries: t.Registries},
		},
		2,
		10,
		false,
		true)
	if err != nil {
		return err
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
		clearRepository(rc.Name, &sc)
	}
	return nil
}

func clearRepository(regName reg.RegistryName,
	sc *reg.SyncContext) {

	mkDeletionCmd := func(
		dest reg.RegistryContext,
		imageName reg.ImageName,
		digest reg.Digest) stream.Producer {
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
	Name       string                `yaml:"name,omitempty"`
	Registries []reg.RegistryContext `yaml:"registries,omitempty"`
	Invocation []string              `yaml:"invocation,omitempty"`
	Snapshots  []RegistrySnapshot    `yaml:"snapshots,omitempty"`
}

// E2ETests is an array of E2ETest.
type E2ETests []E2ETest

// RegistrySnapshot is the snapshot of a registry. It is basically the key/value
// pair out of the reg.MasterInventory type (RegistryName + []Image).
type RegistrySnapshot struct {
	Name   reg.RegistryName `yaml:"name,omitempty"`
	Before []reg.Image      `yaml:"before,omitempty"`
	After  []reg.Image      `yaml:"after,omitempty"`
}

func readE2ETests(filePath string) (E2ETests, error) {
	var ts E2ETests
	b, err := ioutil.ReadFile(filePath)
	if err != nil {
		return ts, err
	}
	if err := yaml.UnmarshalStrict(b, &ts); err != nil {
		return ts, err
	}

	return ts, nil
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

// TODO: Use the version of checkEqual found in
// lib/dockerregistry/inventory_test.go.
func checkEqual(got, expected interface{}) error {
	if !reflect.DeepEqual(got, expected) {
		return fmt.Errorf(
			`<<<<<<< got (type %T)
%v
=======
%v
>>>>>>> expected (type %T)`,
			got,
			got,
			expected,
			expected)
	}
	return nil
}
