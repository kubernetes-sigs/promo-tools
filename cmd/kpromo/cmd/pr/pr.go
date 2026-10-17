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

package pr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/release-sdk/git"
	"sigs.k8s.io/release-sdk/github"
	"sigs.k8s.io/release-utils/command"
	"sigs.k8s.io/release-utils/helpers"

	"sigs.k8s.io/promo-tools/v4/image"
	"sigs.k8s.io/promo-tools/v4/image/consts"
	"sigs.k8s.io/promo-tools/v4/image/manifest"
)

const (
	k8sioRepo             = "k8s.io"
	k8sioDefaultBranch    = "main"
	promotionBranchSuffix = "-image-promotion"
	defaultProject        = consts.StagingRepoSuffix
	defaultReviewers      = "@kubernetes/release-engineering"
	githubHost            = "github.com"

	// maxBodyImages is the maximum number of promoted images named in the
	// PR body.
	maxBodyImages = 3
)

// PRCmd is the kpromo subcommand to promote container images.
var PRCmd = &cobra.Command{
	Use:   "pr",
	Short: "Starts an image promotion for a given image tag",
	Long: `kpromo pr

This command updates image promoter manifests and creates a PR in
kubernetes/k8s.io`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		// Run the PR creation function
		return runPromote(promoteOpts)
	},
}

type promoteOptions struct {
	updateRepo      bool
	useSSH          bool
	interactiveMode bool
	project         string
	stagingRepo     string
	userFork        string
	reviewers       string
	issue           string
	tags            []string
	images          []string
	digests         []string
}

func (o *promoteOptions) Validate() error {
	if len(o.tags) == 0 {
		return errors.New("cannot start promotion --tag is required")
	}

	if o.issue != "" {
		issueURL, err := url.Parse(o.issue)
		if err != nil || (issueURL.Scheme != "http" && issueURL.Scheme != "https") || issueURL.Host != githubHost {
			return fmt.Errorf(
				"--issue must be a full %s URL to the issue (e.g. https://github.com/kubernetes/k8s.io/issues/1234), got %q",
				githubHost, o.issue,
			)
		}
	}

	if o.userFork == "" {
		return errors.New("cannot start promotion --fork is required")
	}

	// Check the fork slug
	if _, _, err := git.ParseRepoSlug(o.userFork); err != nil {
		return fmt.Errorf("checking user's fork: %w", err)
	}

	// Check that the GitHub token is set
	token, isSet := os.LookupEnv(github.TokenEnvKey)
	if !isSet || token == "" {
		return fmt.Errorf("cannot promote images if GitHub token env var %s is not set", github.TokenEnvKey)
	}

	return nil
}

var promoteOpts = &promoteOptions{}

func init() {
	PRCmd.PersistentFlags().StringVar(
		&promoteOpts.project,
		"project",
		defaultProject,
		"the name of the project to promote images for",
	)

	PRCmd.PersistentFlags().StringVar(
		&promoteOpts.stagingRepo,
		"staging-repo",
		"",
		"the full staging repo URL, overrides the source registry read from "+
			"the project's promoter manifest in kubernetes/k8s.io "+
			"(e.g. us-central1-docker.pkg.dev/k8s-staging-images/my-project)",
	)

	PRCmd.PersistentFlags().StringSliceVarP(
		&promoteOpts.tags,
		"tag",
		"t",
		[]string{},
		"version tag of the images we will promote",
	)

	PRCmd.PersistentFlags().StringVar(
		&promoteOpts.userFork,
		"fork",
		"",
		"the user's fork of kubernetes/k8s.io",
	)

	PRCmd.PersistentFlags().StringVar(
		&promoteOpts.reviewers,
		"reviewers",
		defaultReviewers,
		"the list of GitHub users or teams to assign to the PR",
	)

	PRCmd.PersistentFlags().StringVar(
		&promoteOpts.issue,
		"issue",
		"",
		"link to a GitHub issue to cross-reference in the PR body "+
			"(e.g. https://github.com/kubernetes/k8s.io/issues/1234)",
	)

	PRCmd.PersistentFlags().BoolVarP(
		&promoteOpts.interactiveMode,
		"interactive",
		"i",
		false,
		"interactive mode, asks before every step",
	)

	PRCmd.PersistentFlags().StringSliceVarP(
		&promoteOpts.images,
		"image",
		"",
		[]string{},
		"image to promote. If not specified, all images matching the tag will be promoted",
	)

	PRCmd.PersistentFlags().StringSliceVarP(
		&promoteOpts.digests,
		"digests",
		"",
		[]string{},
		"digests to promote. If not specified, all images matching the tag will be promoted",
	)

	PRCmd.PersistentFlags().BoolVarP(
		&promoteOpts.useSSH,
		"use-ssh",
		"",
		true,
		"use ssh to clone the repository, if false will use https (default: true)",
	)

	PRCmd.PersistentFlags().BoolVarP(
		&promoteOpts.updateRepo,
		"update-repo",
		"",
		true,
		"update the cloned repository to fetch any upstream change (default: true)",
	)

	for _, flagName := range []string{"tag", "fork"} {
		if err := PRCmd.MarkPersistentFlagRequired(flagName); err != nil {
			logrus.Errorf("Marking tag %s as required: %v", flagName, err)
		}
	}
}

func runPromote(opts *promoteOptions) error {
	// Check the cmd line opts
	if err := opts.Validate(); err != nil {
		return fmt.Errorf("checking command line options: %w", err)
	}

	ctx := context.Background()

	// Validate options
	branchname := opts.project + "-" + opts.tags[0] + promotionBranchSuffix

	// Get the github org and repo from the fork slug
	userForkOrg, userForkRepo, err := git.ParseRepoSlug(opts.userFork)
	if err != nil {
		return fmt.Errorf("parsing user's fork: %w", err)
	}

	if userForkRepo == "" {
		userForkRepo = k8sioRepo
	}

	// Check Environment
	gh := github.New()

	// Verify the repository is a fork of k8s.io
	if err = github.VerifyFork(
		branchname, userForkOrg, userForkRepo, git.DefaultGithubOrg, k8sioRepo,
	); err != nil {
		return fmt.Errorf("while checking fork of %s/%s: %w ", git.DefaultGithubOrg, k8sioRepo, err)
	}

	// Clone k8s.io
	gitCloneOpts := &gogit.CloneOptions{
		// Set a minimal depth to prevent downloading the whole repository.
		Depth: 1,
	}

	repo, err := github.PrepareFork(branchname, git.DefaultGithubOrg, k8sioRepo, userForkOrg, userForkRepo, opts.useSSH, opts.updateRepo, gitCloneOpts)
	if err != nil {
		return fmt.Errorf("while preparing k/k8s.io fork: %w", err)
	}

	defer func() {
		if mustRun(opts, "Clean fork directory?") {
			err = repo.Cleanup()
		} else {
			logrus.Infof("All modified files will be left untouched in %s", repo.Dir())
		}
	}()

	stagingRepo, err := resolveStagingRepo(opts, filepath.Join(repo.Dir(), consts.ProdRegistry))
	if err != nil {
		return err
	}

	opt := manifest.GrowOptions{}
	if err := opt.Populate(
		filepath.Join(repo.Dir(), consts.ProdRegistry),
		stagingRepo, opts.images, opts.digests, opts.tags); err != nil {
		return fmt.Errorf("populating image promoter options for tag %s with image filter %s: %w", opts.tags, opts.images, err)
	}

	if err := opt.Validate(); err != nil {
		return fmt.Errorf("validate promoter options tag %s with image filter %s: %w", opts.tags, opts.images, err)
	}

	// Find the matching manifest to derive the correct images list path
	mfest, err := manifest.Find(&opt)
	if err != nil {
		return fmt.Errorf("finding manifest for staging repo %s: %w", stagingRepo, err)
	}

	// Path to the promoter image list, derived from the manifest location
	manifestDir := filepath.Base(filepath.Dir(mfest.Filepath))
	imagesListPath := filepath.Join(
		consts.ProdRegistry,
		"images",
		manifestDir,
		"images.yaml",
	)

	// Read the current manifest to check later if new images come up
	oldlist, grown, err := growManifest(ctx, opts, &opt, filepath.Join(repo.Dir(), imagesListPath))
	if err != nil {
		return err
	}

	// Re-write the image list without the mock images
	rawImageList, err := image.NewManifestListFromFile(filepath.Join(repo.Dir(), imagesListPath))
	if err != nil {
		return fmt.Errorf("parsing the current manifest: %w", err)
	}

	// Create a new imagelist to copy the non-mock images
	newImageList := &image.ManifestList{}

	// Copy all non mock-images:
	for _, imageData := range *rawImageList {
		if !strings.Contains(imageData.Name, "mock/") {
			*newImageList = append(*newImageList, imageData)
		}
	}

	// Write the modified manifest
	if err := newImageList.Write(filepath.Join(repo.Dir(), imagesListPath)); err != nil {
		return fmt.Errorf("while writing the promoter image list: %w", err)
	}

	// Check if the image list was modified
	if len(oldlist) > 0 {
		logrus.Debug("Checking if the image list was modified")
		// read the newly modified manifest
		newlist, err := os.ReadFile(filepath.Join(repo.Dir(), imagesListPath))
		if err != nil {
			return fmt.Errorf("while reading the modified manifest images list: %w", err)
		}

		// If the manifest was not modified, exit now
		if bytes.Equal(newlist, oldlist) {
			logrus.Info("No changes detected in the promoter images list, exiting without changes")

			return nil
		}
	}

	// add the modified manifest to staging
	logrus.Debugf("Adding %s to staging area", imagesListPath)

	if err := disableIndexSkipHash(repo.Dir()); err != nil {
		return fmt.Errorf("configuring the git index of %s: %w", repo.Dir(), err)
	}

	if err := repo.Add(imagesListPath); err != nil {
		return fmt.Errorf("adding image manifest to staging area: %w", err)
	}

	// Without growing, oldlist is empty and every image would count as promoted.
	var promoted []string
	if grown {
		promoted = promotedImages(oldlist, newImageList)
	}

	commitMessage := prTitle(opts, promoted)

	// Commit files
	logrus.Debug("Creating commit")

	if err := repo.UserCommit(commitMessage); err != nil {
		return fmt.Errorf("creating commit in %s/%s: %w", git.DefaultGithubOrg, k8sioRepo, err)
	}

	// Push to fork
	if mustRun(opts, fmt.Sprintf("Push changes to user's fork at %s/%s?", userForkOrg, userForkRepo)) {
		logrus.Infof("Pushing manifest changes to %s/%s", userForkOrg, userForkRepo)

		if err := repo.PushToRemote(github.UserForkName, branchname); err != nil {
			return fmt.Errorf("pushing %s to %s/%s: %w", github.UserForkName, userForkOrg, userForkRepo, err)
		}
	} else {
		// Exit if no push was made
		logrus.Infof("Exiting without creating a PR since changes were not pushed to %s/%s", userForkOrg, userForkRepo)

		return nil
	}

	// Create the Pull Request
	if mustRun(opts, "Create pull request?") {
		pr, err := gh.CreatePullRequest(
			git.DefaultGithubOrg, k8sioRepo, k8sioDefaultBranch,
			fmt.Sprintf("%s:%s", userForkOrg, branchname),
			commitMessage, generatePRBody(opts, promoted), false,
		)
		if err != nil {
			return fmt.Errorf("creating the pull request in k/k8s.io: %w", err)
		}

		logrus.Infof(
			"Successfully created PR: %s%s/%s/pull/%d",
			github.GitHubURL, git.DefaultGithubOrg, k8sioRepo, pr.GetNumber(),
		)
	}

	// Success!
	return nil
}

// growManifest runs the promoter manifest grower unless the user declines it.
// It returns the image list at imagesListPath from before growing, and whether
// the manifest was grown.
func growManifest(
	ctx context.Context, opts *promoteOptions, opt *manifest.GrowOptions, imagesListPath string,
) ([]byte, bool, error) {
	oldlist := make([]byte, 0)

	if !mustRun(opts, "Grow the manifests to add the new tags?") {
		return oldlist, false, nil
	}

	if helpers.Exists(imagesListPath) {
		logrus.Debug("Reading the current image promoter manifest (image list)")

		var err error

		oldlist, err = os.ReadFile(imagesListPath)
		if err != nil {
			return nil, false, fmt.Errorf("while reading the current promoter image list: %w", err)
		}
	}

	logrus.Infof("Growing manifests for images matching filter %s and matching tag %s", opts.images, opts.tags)

	if err := manifest.Grow(ctx, opt); err != nil {
		return nil, false, fmt.Errorf("growing manifest with image filter %s and tag %s: %w", opts.images, opts.tags, err)
	}

	return oldlist, true, nil
}

// resolveStagingRepo returns the staging repo to promote images from.
// It returns the --staging-repo value if set. Otherwise, it reads the source
// registry from the project's promoter manifest in baseDir.
func resolveStagingRepo(opts *promoteOptions, baseDir string) (string, error) {
	if opts.stagingRepo != "" {
		return opts.stagingRepo, nil
	}

	srcRegistry, err := manifest.FindStagingRepo(baseDir, opts.project)
	if err != nil {
		return "", fmt.Errorf(
			"determining staging repo for project %s: %w",
			opts.project, err,
		)
	}

	logrus.Infof("Using staging repo %s from the promoter manifest of project %s", srcRegistry, opts.project)

	return string(srcRegistry), nil
}

// promotedImages returns the sorted names of the images in newList that have
// digests or tags not present in the oldList YAML.
func promotedImages(oldList []byte, newList *image.ManifestList) []string {
	old := &image.ManifestList{}
	if err := old.Parse(oldList); err != nil {
		logrus.Warnf("Parsing the previous promoter image list: %v", err)

		return nil
	}

	oldDMaps := make(map[string]map[string][]string, len(*old))
	for _, img := range *old {
		oldDMaps[img.Name] = img.DMap
	}

	images := []string{}

	for _, img := range *newList {
		oldDMap := oldDMaps[img.Name]

		for digest, tags := range img.DMap {
			oldTags, found := oldDMap[digest]
			if !found || slices.ContainsFunc(tags, func(tag string) bool {
				return !slices.Contains(oldTags, tag)
			}) {
				images = append(images, img.Name)

				break
			}
		}
	}

	slices.Sort(images)

	return withoutArchVariants(images)
}

// archSuffixes are the suffixes of architecture specific image names, e.g.
// foo-amd64 for the foo manifest list.
var archSuffixes = []string{
	"-386", "-amd64", "-arm", "-arm32v7", "-arm64", "-arm64v8", "-ppc64le", "-riscv64", "-s390x",
}

// withoutArchVariants drops the architecture specific images whose base image
// is also in images, so a multi-arch image is named only once.
func withoutArchVariants(images []string) []string {
	promoted := sets.New(images...)

	return slices.DeleteFunc(slices.Clone(images), func(img string) bool {
		for _, suffix := range archSuffixes {
			if base, found := strings.CutSuffix(img, suffix); found && promoted.Has(base) {
				return true
			}
		}

		return false
	})
}

// prTitle returns the title of the promotion PR, also used as the commit
// message. It names the promoted image if there is only one.
func prTitle(opts *promoteOptions, images []string) string {
	subject := opts.project
	if len(images) == 1 {
		subject += "/" + images[0]
	}

	title := "Image promotion for " + subject + " " + strings.Join(opts.tags, " / ")
	if opts.project == consts.StagingRepoSuffix {
		title = "releng: " + title
	}

	return title
}

// disableIndexSkipHash makes git write the index checksum in the repository
// at dir. The manifest is added with the git CLI but committed with go-git,
// which rejects an index without checksum as written with index.skipHash,
// for example when the user enabled feature.manyFiles.
func disableIndexSkipHash(dir string) error {
	if err := command.NewWithWorkDir(
		dir, "git", "config", "--local", "index.skipHash", "false",
	).RunSilentSuccess(); err != nil {
		return fmt.Errorf("disabling index.skipHash: %w", err)
	}

	return nil
}

// mustRun avoids running when a users chooses n in interactive mode.
func mustRun(opts *promoteOptions, question string) bool {
	if !opts.interactiveMode {
		return true
	}

	_, success, err := helpers.Ask(question+" (Y/n)", "y:Y:yes|n:N:no|y", 10)
	if err != nil {
		logrus.Error(err)

		//nolint:errcheck // only the type match matters, err is already logged
		if _, ok := errors.AsType[helpers.UserInputError](err); ok {
			os.Exit(1)
		}

		return false
	}

	if success {
		return true
	}

	return false
}

// generatePRBody creates the body of the Image Promotion Pull Request.
func generatePRBody(opts *promoteOptions, images []string) string {
	args := "--fork " + opts.userFork
	if opts.interactiveMode {
		args += " --interactive"
	}

	if opts.project != defaultProject {
		args += " --project " + opts.project
	}

	if opts.stagingRepo != "" {
		args += " --staging-repo " + opts.stagingRepo
	}

	if opts.reviewers != defaultReviewers {
		args += " --reviewers \"" + opts.reviewers + "\""
	}

	if opts.issue != "" {
		args += " --issue \"" + opts.issue + "\""
	}

	var tagString strings.Builder
	for _, tag := range opts.tags {
		tagString.WriteString(" --tag " + tag)
	}

	args += tagString.String()

	var imageString strings.Builder

	for _, filterImage := range opts.images {
		if filterImage != "" {
			imageString.WriteString(" --image " + filterImage)
		}
	}

	args += imageString.String()

	subject := opts.project
	if len(images) > 0 && len(images) <= maxBodyImages {
		subject += " (" + strings.Join(images, ", ") + ")"
	}

	prBody := fmt.Sprintf("Image promotion for %s %s\n", subject, strings.Join(opts.tags, " / "))

	if opts.issue != "" {
		prBody += "\nxref: " + opts.issue + "\n\n"
	}

	prBody += "This is an automated PR generated from `kpromo`\n"
	prBody += fmt.Sprintf("```\nkpromo pr %s\n```\n\n", args)
	prBody += fmt.Sprintf("/hold\ncc: %s\n", opts.reviewers)

	return prBody
}
