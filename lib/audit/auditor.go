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

package audit

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	git "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"k8s.io/klog"

	reg "sigs.k8s.io/k8s-container-image-promoter/lib/dockerregistry"
)

// ServerContext holds all of the initialization data for the server to start
// up.
type ServerContext struct {
	RepoURL             *url.URL
	RepoBranch          string
	ThinManifestDirPath string
	Repo                *git.Repository
	RepoPath            string
}

// PubSubMessageInner is the inner struct that holds the actual Pub/Sub
// information.
type PubSubMessageInner struct {
	Data []byte `json:"data,omitempty"`
	ID   string `json:"id"`
}

// PubSubMessage is the payload of a Pub/Sub event.
type PubSubMessage struct {
	Message      PubSubMessageInner `json:"message"`
	Subscription string             `json:"subscription"`
}

func initServerContext(
	repoURLStr, branch, path string,
) (*ServerContext, error) {

	repoURL, err := url.Parse(repoURLStr)
	if err != nil {
		return nil, err
	}

	// This creates a full-blown clone to a temp dir.
	r, repoPath, err := cloneToTempDir(repoURL, branch)
	if err != nil {
		return nil, err
	}

	serverContext := ServerContext{
		RepoURL:             repoURL,
		RepoBranch:          branch,
		ThinManifestDirPath: path,
		Repo:                r,
		RepoPath:            repoPath,
	}

	return &serverContext, nil
}

// Auditor runs an HTTP server.
func Auditor(repoURL, branch, path string) {
	klog.Info("Starting Auditor")
	serverContext, err := initServerContext(repoURL, branch, path)
	if err != nil {
		klog.Exitln(err)
	}

	klog.Infoln(serverContext)
	klog.Infoln(serverContext.Repo)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serverContext.Audit(w, r)
	})
	// Determine port for HTTP service.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		klog.Infof("Defaulting to port %s", port)
	}
	// Start HTTP server.
	klog.Infof("Listening on port %s", port)
	klog.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), nil))
}

func cloneToTempDir(
	repoURL fmt.Stringer,
	branch string,
) (*git.Repository, string, error) {
	tdir, err := ioutil.TempDir("", "k8s.io-")
	if err != nil {
		return nil, "", err
	}

	r, err := git.PlainClone(tdir, false, &git.CloneOptions{
		URL:           repoURL.String(),
		ReferenceName: (plumbing.ReferenceName)(branch),
	})
	if err != nil {
		return nil, "", err
	}

	sha, err := getHeadSha(r)
	if err == nil {
		klog.Infof("updated %v to revision %v", tdir, sha)
	}

	return r, tdir, nil
}

// Check if the given path already has a git repository. If it does, just use
// that. Otherwise, clone a repo to a new temporary path.
func cloneOrPull(repoURL fmt.Stringer,
	branch,
	repoPath string,
) (*git.Repository, string, error) {
	r, err := git.PlainOpen(repoPath)
	if err == nil {
		// Pull
		wt, err := r.Worktree()
		if err != nil {
			klog.Error("worktree error!! can't retrieve it!", err)
			return r, repoPath, err
		}

		klog.Info("pulling")
		err = wt.Pull(&git.PullOptions{RemoteName: "origin"})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			klog.Error(err)
			return r, repoPath, err
		}
		klog.Info("pull OK")

		sha, err := getHeadSha(r)
		if err == nil {
			klog.Infof("updated %v to revision %v", repoPath, sha)
		}
		return r, repoPath, nil
	}

	return cloneToTempDir(repoURL, branch)
}

func getHeadSha(repo *git.Repository) (string, error) {
	head, err := repo.Head()
	if err != nil {
		return "", err
	}

	return head.Hash().String(), nil
}

// ParsePubSubMessage parses an HTTP request body into a reg.GCRPubSubPayload.
func ParsePubSubMessage(r *http.Request) (*reg.GCRPubSubPayload, error) {
	var psm PubSubMessage
	var gcrPayload reg.GCRPubSubPayload

	// Handle basic errors (malformed requests).
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("iotuil.ReadAll: %v", err)
	}
	if err := json.Unmarshal(body, &psm); err != nil {
		return nil, fmt.Errorf("json.Unmarshal: %v", err)
	}

	if err := json.Unmarshal(psm.Message.Data, &gcrPayload); err != nil {
		return nil, fmt.Errorf("json.Unmarshal: %v", err)
	}

	if len(gcrPayload.Digest) == 0 && len(gcrPayload.Tag) == 0 {
		return nil, fmt.Errorf(
			"gcrPayload: neither Digest nor Tag was specified")
	}

	switch gcrPayload.Action {
	case "":
		return nil, fmt.Errorf("gcrPayload: Action not specified")
	// All deletions will for now be treated as an error. If it's an insertion,
	// it can either have "digest" with FQIN, or "digest" + "tag" with PQIN. So
	// we always verify FQIN, and if there is PQIN, verify that as well.
	case "DELETE":
		// Even though this is an error, we successfully processed this message,
		// so exit with an error.
		return nil, fmt.Errorf(
			"TRANSACTION REJECTED: %v: deletions are prohibited", gcrPayload)
	case "INSERT":
		return &gcrPayload, nil
	default:
		return nil, fmt.Errorf(
			"gcrPayload: unknown action %q", gcrPayload.Action)
	}
}

// Audit receives and processes a Pub/Sub push message. It has 3 parts: (1)
// parse the request body to understand the GCR state change, (2) update the Git
// repo of the promoter manifests, and (3) reconcile these two against each
// other.
// nolint[funlen]
func (s *ServerContext) Audit(w http.ResponseWriter, r *http.Request) {
	// (1) Parse request payload.
	gcrPayload, err := ParsePubSubMessage(r)
	if err != nil {
		// It's important to fail any message we cannot parse, because this
		// notifies us of any changes in how the messages are created in the
		// first place.
		msg := fmt.Sprintf("TRANSACTION REJECTED: parse failure: %v", err)
		klog.Errorf(msg)
		_, _ = w.Write([]byte(msg))
		return
	}

	// (2) Re-pull the existing git repo (or clone it if it doesn't exist).
	s.Repo, s.RepoPath, err = cloneOrPull(s.RepoURL, s.RepoBranch, s.RepoPath)
	if err != nil {
		klog.Error(err)
		// Return an HTTP error, so that this pubsub message may get retried
		// (this is the behavior of Cloud Run). We need to retry it because a
		// well-formed request should not fail to be processed because of a
		// separate Git syncing failure.
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Debug info.
	klog.Infof("gcrPayload: %v", gcrPayload)
	klog.Infof("s.RepoURL: %v", s.RepoURL)
	klog.Infof("s.RepoBranch: %v", s.RepoBranch)
	klog.Infof("s.ThinManifestDirPath: %v", s.ThinManifestDirPath)
	klog.Infof("s.Repo: %v", s.Repo)
	klog.Infof("s.RepoPath: %v", s.RepoPath)

	mfests, err := reg.ParseManifestsFromDir(
		filepath.Join(s.RepoPath, s.ThinManifestDirPath),
		reg.ParseThinManifestFromFile)
	if err != nil {
		klog.Error(err)
		// Similar to the error from cloneOrPull(), respond with an HTTP error
		// so that the Pub/Sub message may be retried. This gracefully handles
		// the case where the Pub/Sub message is well-formed, but the promoter
		// manifests are in a bad state (perhaps due to a faulty merge (e.g., a
		// merge that forgot to remove conflict markers)).
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// (3) Compare GCR state change with the intent of the promoter manifests.
	for _, mfest := range mfests {
		if mfest.Contains(*gcrPayload) {
			msg := fmt.Sprintf(
				"TRANSACTION VERIFIED: %v: agrees with manifest\n", gcrPayload)
			klog.Info(msg)
			_, _ = w.Write([]byte(msg))
			return
		}
	}

	msg := fmt.Sprintf(
		"TRANSACTION REJECTED: %v: could not validate", gcrPayload)
	klog.Error(msg)
	// Return 200 OK, because we don't want to re-process this transaction.
	// "Terminating" the auditing here simplifies debugging as well, because the
	// same message is not repeated over and over again in the logs.
	_, _ = w.Write([]byte(msg))
}
