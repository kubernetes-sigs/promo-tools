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

package inventory

import (
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"

	gogit "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
)

func getGitShaFromEnv(envVar string) (plumbing.Hash, error) {
	potenitalSHA := os.Getenv(envVar)
	const gitShaLength = 40
	if len(potenitalSHA) != gitShaLength {
		return plumbing.Hash{},
			fmt.Errorf("Length of SHA is %d characters, should be %d",
				len(potenitalSHA), gitShaLength)
	}
	_, err := hex.DecodeString(potenitalSHA)
	if err != nil {
		return plumbing.Hash{}, fmt.Errorf("Not a valid SHA: %v", err)
	}
	return plumbing.NewHash(potenitalSHA), nil
}

// MKRealImageRemovalCheck returns an instance of ImageRemovalCheck.
func MKRealImageRemovalCheck(
	gitRepoPath string,
	edges map[PromotionEdge]interface{},
) (*ImageRemovalCheck, error) {
	// The "PULL_BASE_SHA" and "PULL_PULL_SHA" environment variables are given
	// by the PROW job running the promoter container and represent the Git SHAs
	// for the master branch and the pull request branch respectively.
	masterSHA, err := getGitShaFromEnv("PULL_BASE_SHA")
	if err != nil {
		return nil, fmt.Errorf("The PULL_BASE_SHA environment variable "+
			"is invalid: %v", err)
	}
	pullRequestSHA, err := getGitShaFromEnv("PULL_PULL_SHA")
	if err != nil {
		return nil, fmt.Errorf("The PULL_PULL_SHA environment variable "+
			"is invalid: %v", err)
	}
	return &ImageRemovalCheck{
		gitRepoPath,
		masterSHA,
		pullRequestSHA,
		edges,
	}, nil
}

// Run executes ImageRemovalCheck on a set of promotion edges.
// Returns an error if the pull request removes images from the
// promoter manifests.
func (check *ImageRemovalCheck) Run() error {
	r, err := gogit.PlainOpen(check.GitRepoPath)
	if err != nil {
		return fmt.Errorf("Could not open the Git repo: %v", err)
	}
	w, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("Could not create Git worktree: %v", err)
	}

	// The Prow job that this check is running in has already cloned the
	// git repo for us so we can just checkout the master branch to get the
	// master branch's version of the promoter manifests.
	err = w.Checkout(&gogit.CheckoutOptions{
		Hash:  check.MasterSHA,
		Force: true,
	})
	if err != nil {
		return fmt.Errorf("Could not checkout the master branch of the Git"+
			" repo: %v", err)
	}

	mfests, err := ParseThinManifestsFromDir(check.GitRepoPath)
	if err != nil {
		return fmt.Errorf("Could not parse manifests from the directory: %v",
			err)
	}
	masterEdges, err := ToPromotionEdges(mfests)
	if err != nil {
		return fmt.Errorf("Could not generate promotion edges from promoter"+
			" manifests: %v", err)
	}

	// Reset the current directory back to the pull request branch so that this
	// check doesn't leave lasting effects that could affect subsequent checks.
	err = w.Checkout(&gogit.CheckoutOptions{
		Hash:  check.PullRequestSHA,
		Force: true,
	})
	if err != nil {
		return fmt.Errorf("Could not checkout the pull request branch of the"+
			" Git repo %v: %v",
			check.GitRepoPath, err)
	}

	return check.Compare(masterEdges, check.PullEdges)
}

// Compare is a function of the ImageRemovalCheck that handles
// the comparison of the pull requests's set of promotion edges and
// the master branch's set of promotion edges.
func (check *ImageRemovalCheck) Compare(
	edgesMaster map[PromotionEdge]interface{},
	edgesPullRequest map[PromotionEdge]interface{},
) error {
	// Generate a set of all destination images that appear in
	// the pull request's set of promotion edges.
	destinationImages := make(map[PromotionEdge]interface{})
	for edge := range edgesPullRequest {
		destinationImages[PromotionEdge{
			DstImageTag: edge.DstImageTag,
			Digest:      edge.Digest,
		}] = nil
	}

	// Check that every destination image in the master branch's set of
	// promotion edges exists in the pull request's set of promotion edges.
	removedImages := make([]string, 0)
	for edge := range edgesMaster {
		_, found := destinationImages[PromotionEdge{
			DstImageTag: edge.DstImageTag,
			Digest:      edge.Digest,
		}]
		if !found {
			removedImages = append(removedImages,
				string(edge.DstImageTag.ImageName))
		}
	}

	if len(removedImages) > 0 {
		return fmt.Errorf("The following images were removed in this pull "+
			"request: %v", strings.Join(removedImages, ", "))
	}
	return nil
}
