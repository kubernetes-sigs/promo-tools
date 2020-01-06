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
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/logging"
	git "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"k8s.io/klog"
	reg "sigs.k8s.io/k8s-container-image-promoter/lib/dockerregistry"
)

// ServerContext holds all of the initialization data for the server to start
// up.
type ServerContext struct {
	RepoURL              *url.URL
	RepoBranch           string
	ThinManifestDirPath  string
	ErrorReportingClient *errorreporting.Client
	LogClient            *logging.Client
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

const (
	cloneDepth = 1
)

func initServerContext(
	gcpProjectID, repoURLStr, branch, path string,
) (*ServerContext, error) {

	repoURL, err := url.Parse(repoURLStr)
	if err != nil {
		return nil, err
	}

	erc := initErrorReportingClient(gcpProjectID)
	logClient := initLogClient(gcpProjectID)

	serverContext := ServerContext{
		RepoURL:              repoURL,
		RepoBranch:           branch,
		ThinManifestDirPath:  path,
		ErrorReportingClient: erc,
		LogClient:            logClient,
	}

	return &serverContext, nil
}

// initLogClient creates a logging client that performs better logging than the
// default behavior on GCP Stackdriver. For instance, logs sent with this client
// are not split up over newlines, and also the severity levels are actually
// understood by Stackdriver.
func initLogClient(projectID string) *logging.Client {

	ctx := context.Background()

	// Creates a client.
	client, err := logging.NewClient(ctx, projectID)
	if err != nil {
		klog.Fatalf("Failed to create client: %v", err)
	}

	return client
}

func initErrorReportingClient(projectID string) *errorreporting.Client {

	ctx := context.Background()

	erc, err := errorreporting.NewClient(ctx, projectID, errorreporting.Config{
		ServiceName: "cip-auditor",
		OnError: func(err error) {
			klog.Errorf("Could not log error: %v", err)
		},
	})
	if err != nil {
		klog.Fatalf("Failed to create errorreporting client: %v", err)
	}

	return erc
}

// Auditor runs an HTTP server.
func Auditor(gcpProjectID, repoURL, branch, path string) {
	klog.Info("Starting Auditor")
	serverContext, err := initServerContext(gcpProjectID, repoURL, branch, path)
	if err != nil {
		klog.Exitln(err)
	}

	klog.Infoln(serverContext)

	// nolint[errcheck]
	defer serverContext.LogClient.Close()
	// nolint[errcheck]
	defer serverContext.ErrorReportingClient.Close()

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
) (string, error) {
	tdir, err := ioutil.TempDir("", "k8s.io-")
	if err != nil {
		return "", err
	}

	r, err := git.PlainClone(tdir, false, &git.CloneOptions{
		URL:           repoURL.String(),
		ReferenceName: (plumbing.ReferenceName)("refs/heads/" + branch),
		Depth:         cloneDepth,
	})
	if err != nil {
		return "", err
	}

	sha, err := getHeadSha(r)
	if err == nil {
		klog.Infof("cloned %v at revision %v", tdir, sha)
	}

	return tdir, nil
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
			"%v: deletions are prohibited", gcrPayload)
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
	logInfo := s.LogClient.Logger("audit-log").StandardLogger(logging.Info)
	logError := s.LogClient.Logger("audit-log").StandardLogger(logging.Error)
	logAlert := s.LogClient.Logger("audit-log").StandardLogger(logging.Alert)

	defer func() {
		if msg := recover(); msg != nil {
			panicStr := msg.(string)

			stacktrace := debug.Stack()

			s.ErrorReportingClient.Report(errorreporting.Entry{
				Req:   r,
				Error: fmt.Errorf("%s", panicStr),
				Stack: stacktrace,
			})

			logAlert.Printf("%s\n%s\n", panicStr, string(stacktrace))

		}
	}()
	// (1) Parse request payload.
	gcrPayload, err := ParsePubSubMessage(r)
	if err != nil {
		// It's important to fail any message we cannot parse, because this
		// notifies us of any changes in how the messages are created in the
		// first place.
		msg := fmt.Sprintf("TRANSACTION REJECTED: parse failure: %v", err)
		_, _ = w.Write([]byte(msg))
		panic(msg)
	}

	// (2) Clone fresh repo.
	var repoPath string
	repoPath, err = cloneToTempDir(s.RepoURL, s.RepoBranch)
	if err != nil {
		logError.Println(err)
		// Return an HTTP error, so that this pubsub message may get retried
		// (this is the behavior of Cloud Run). We need to retry it because a
		// well-formed request should not fail to be processed because of a
		// separate Git syncing failure.
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Debug info.
	logInfo.Printf("gcrPayload: %v", gcrPayload)
	logInfo.Printf("s.RepoURL: %v", s.RepoURL)
	logInfo.Printf("s.RepoBranch: %v", s.RepoBranch)
	logInfo.Printf("s.ThinManifestDirPath: %v", s.ThinManifestDirPath)
	logInfo.Printf("s.RepoPath: %v", repoPath)

	mfests, err := reg.ParseThinManifestsFromDir(
		filepath.Join(repoPath, s.ThinManifestDirPath))
	if err != nil {
		logError.Println(err)
		// Similar to the error from cloneToTempDir(), respond with an HTTP error
		// so that the Pub/Sub message may be retried. This gracefully handles
		// the case where the Pub/Sub message is well-formed, but the promoter
		// manifests are in a bad state (perhaps due to a faulty merge (e.g., a
		// merge that forgot to remove conflict markers)).
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Garbage-collect freshly-cloned repo (we don't need it any more).
	err = os.RemoveAll(repoPath)
	if err != nil {
		// We don't really care too much about failures about removing the
		// (temporary) repoPath directory, because we'll clone a fresh one
		// anyway in the future.
		klog.Errorf("Could not remove temporary Git repo %v: %v", repoPath, err)
	}

	// (3) Compare GCR state change with the intent of the promoter manifests.
	for _, mfest := range mfests {
		if mfest.Contains(*gcrPayload) {
			msg := fmt.Sprintf(
				"TRANSACTION VERIFIED: %v: agrees with manifest\n", gcrPayload)
			logInfo.Println(msg)
			_, _ = w.Write([]byte(msg))
			return
		}
	}

	msg := fmt.Sprintf(
		"TRANSACTION REJECTED: %v: could not validate", gcrPayload)
	// Return 200 OK, because we don't want to re-process this transaction.
	// "Terminating" the auditing here simplifies debugging as well, because the
	// same message is not repeated over and over again in the logs.
	_, _ = w.Write([]byte(msg))
	panic(msg)
}
