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
	"os"
	"runtime/debug"
	"strings"

	"cloud.google.com/go/errorreporting"
	"k8s.io/klog"
	reg "sigs.k8s.io/k8s-container-image-promoter/lib/dockerregistry"
	"sigs.k8s.io/k8s-container-image-promoter/lib/logclient"
	"sigs.k8s.io/k8s-container-image-promoter/lib/remotemanifest"
	"sigs.k8s.io/k8s-container-image-promoter/lib/report"
)

// InitRealServerContext creates a ServerContext with facilities that are meant
// for production use (going over the network to fetch actual official promoter
// manifests from GitHub, for example).
func InitRealServerContext(
	gcpProjectID, repoURLStr, branch, path, uuid string,
) (*ServerContext, error) {

	remoteManifestFacility, err := remotemanifest.NewGit(
		repoURLStr,
		branch,
		path)
	if err != nil {
		return nil, err
	}
	reportingFacility := report.NewGcpErrorReportingClient(
		gcpProjectID, "cip-auditor")
	loggingFacility, err := logclient.NewGcpLogClient(
		gcpProjectID, LogName)
	if err != nil {
		return nil, err
	}

	serverContext := ServerContext{
		ID:                     uuid,
		RemoteManifestFacility: remoteManifestFacility,
		ErrorReportingFacility: reportingFacility,
		LoggingFacility:        loggingFacility,
		GcrReadingFacility: GcrReadingFacility{
			ReadRepo:         reg.MkReadRepositoryCmdReal,
			ReadManifestList: reg.MkReadManifestListCmdReal,
		},
	}

	return &serverContext, nil
}

// RunAuditor runs an HTTP server.
func (s *ServerContext) RunAuditor() {
	klog.Info("Starting Auditor")
	klog.Infoln(s)

	// nolint[errcheck]
	defer s.LoggingFacility.Close()
	// nolint[errcheck]
	defer s.ErrorReportingFacility.Close()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.Audit(w, r)
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
	logInfo := s.LoggingFacility.GetInfoLogger()
	logError := s.LoggingFacility.GetErrorLogger()
	logAlert := s.LoggingFacility.GetAlertLogger()

	defer func() {
		if msg := recover(); msg != nil {
			panicStr := msg.(string)

			stacktrace := debug.Stack()

			s.ErrorReportingFacility.Report(errorreporting.Entry{
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
		msg := fmt.Sprintf("(%s) TRANSACTION REJECTED: parse failure: %v", s.ID, err)
		_, _ = w.Write([]byte(msg))
		panic(msg)
	}

	msg := fmt.Sprintf(
		"(%s) HANDLING MESSAGE: %v\n", s.ID, gcrPayload)
	logInfo.Println(msg)

	// (2) Clone fresh repo (or use one already on disk).
	manifests, err := s.RemoteManifestFacility.Fetch()
	if err != nil {
		logError.Println(err)
		// If there is an error, return an HTTP error so that the Pub/Sub
		// message may be retried (this is a behavior of Cloud Run's handling of
		// Pub/Sub messages that are converted into HTTP messages).
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Debug info.
	logInfo.Printf("(%s) gcrPayload: %v", s.ID, gcrPayload)
	logInfo.Printf("(%s) RemoteManifestFacility: %v", s.ID, s.RemoteManifestFacility)

	// (3) Compare GCR state change with the intent of the promoter manifests
	// (exact digest match on a tagged or tagless image).
	for _, manifest := range manifests {
		if manifest.Contains(*gcrPayload)&(reg.FlagDigestMatched|reg.FlagTagMatched) != 0 {
			msg := fmt.Sprintf(
				"(%s) TRANSACTION VERIFIED: %v: agrees with manifest\n", s.ID, gcrPayload)
			logInfo.Println(msg)
			_, _ = w.Write([]byte(msg))
			return
		}
	}

	// (4) It could be that the manifest is a child manifest (part of a fat
	// manifest). This is the case where the user only specifies the digest of
	// the parent image, but not the child image. When the promoter copies over
	// the entirety of the fat manifest, it will necessarily copy over the child
	// images as part of the transaction. To validate child images, we have to
	// first scan the source repository (from where the child image is being
	// promoted from) and then run reg.ReadGCRManifestLists to populate the
	// parent/child relationship maps of all relevant fat manifests.
	//
	// Because the subproject is gcr.io/k8s-artifacts-prod/<subproject>/foo...,
	// we can search for the matching subproject and run
	// reg.ReadGCRManifestLists.
	sc, err := reg.MakeSyncContext(
		manifests,
		// verbosity
		2,
		// threads
		10,
		// dry run (although not necessary as we'll only be doing image reads,
		// it doesn't hurt)
		true,
		// useServiceAccount
		false)
	if err != nil {
		// Retry Pub/Sub message if the above fails (it shouldn't because
		// MakeSyncContext can only error out if the useServiceAccount bool is
		// set to True).
		logError.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Find the subproject's registry.
	var srcRegistry reg.RegistryContext
	for _, rc := range sc.RegistryContexts {
		if !rc.Src {
			continue
		}
		rcNameParts := strings.Split(string(rc.Name), "/")
		if len(rcNameParts) < 3 {
			continue
		}
		// Find the subproject key from the pub/sub message.
		klog.Infof("subproject name: %s", rcNameParts[2])
		if strings.HasSuffix(string(rc.Name), rcNameParts[2]) {
			rcBound := rc // Avoid loop reference variable.
			srcRegistry = rcBound
			break
		}
	}
	// If we can't find the source registry for this image, then reject the
	// transaction.
	if string(srcRegistry.Name) == "" {
		msg := fmt.Sprintf("(%s) TRANSACTION REJECTED: could not determine source registry: %v", s.ID, gcrPayload)
		_, _ = w.Write([]byte(msg))
		panic(msg)
	}
	sc.ReadRegistries(
		[]reg.RegistryContext{srcRegistry},
		true,
		s.GcrReadingFacility.ReadRepo)
	sc.ReadGCRManifestLists(s.GcrReadingFacility.ReadManifestList)
	klog.Infof("sc.ParentDigest is: %v", sc.ParentDigest)
	var childDigest reg.Digest
	childImageParts := strings.Split(gcrPayload.Digest, "@")
	if len(childImageParts) != 2 {
		msg := fmt.Sprintf("(%s) TRANSACTION REJECTED: could not split child digest information: %v", s.ID, gcrPayload.Digest)
		_, _ = w.Write([]byte(msg))
		panic(msg)
	}
	childDigest = reg.Digest(childImageParts[1])
	klog.Infof("looking for child digest %v", childDigest)
	if parentDigest, hasParent := sc.ParentDigest[childDigest]; hasParent {
		msg := fmt.Sprintf(
			"(%s) TRANSACTION VERIFIED: %v: agrees with manifest (parent digest %v)\n", s.ID, gcrPayload, parentDigest)
		logInfo.Println(msg)
		_, _ = w.Write([]byte(msg))
		return
	}

	// (5) If all of the above checks fail, then this transaction is unable tobe
	// verified.
	msg = fmt.Sprintf(
		"(%s) TRANSACTION REJECTED: %v: could not validate", s.ID, gcrPayload)
	// Return 200 OK, because we don't want to re-process this transaction.
	// "Terminating" the auditing here simplifies debugging as well, because the
	// same message is not repeated over and over again in the logs.
	_, _ = w.Write([]byte(msg))
	panic(msg)
}
