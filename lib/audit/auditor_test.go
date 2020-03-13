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
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	reg "sigs.k8s.io/k8s-container-image-promoter/lib/dockerregistry"
	"sigs.k8s.io/k8s-container-image-promoter/lib/logclient"
	"sigs.k8s.io/k8s-container-image-promoter/lib/remotemanifest"
	"sigs.k8s.io/k8s-container-image-promoter/lib/report"
	"sigs.k8s.io/k8s-container-image-promoter/lib/stream"
)

func checkMatch(haystack []byte, re *regexp.Regexp) error {
	if !re.Match(haystack) {
		return fmt.Errorf(
			"===BEGIN-MESSAGE===\nCOULD NOT FIND MATCH FOR %q IN\n%s\n===END-MESSAGE===",
			re.String(),
			haystack)
	}
	return nil
}

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

func checkError(t *testing.T, err error, msg string) {
	if err != nil {
		fmt.Printf("\n%v", msg)
		fmt.Println(err)
		fmt.Println()
		t.Fail()
	}
}

func TestParsePubSubMessage(t *testing.T) {
	shouldBeValid := []reg.GCRPubSubPayload{
		{
			Action: "INSERT",
			FQIN:   "gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			Action: "INSERT",
			FQIN:   "gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000",
			PQIN:   "gcr.io/foo/bar:1.0",
		},
	}

	inputToHTTPReq := func(input reg.GCRPubSubPayload) *http.Request {
		b, err := json.Marshal(&input)
		if err != nil {
			fmt.Println("11111111")
			t.Fail()
		}
		psm := PubSubMessage{
			Message: PubSubMessageInner{
				Data: b,
				ID:   "1"},
			Subscription: "2"}

		psmBytes, err := json.Marshal(psm)
		if err != nil {
			fmt.Println("22222222")
			t.Fail()
		}

		return &http.Request{
			Body: ioutil.NopCloser(strings.NewReader((string)(psmBytes)))}
	}

	for _, input := range shouldBeValid {
		_, gotErr := ParsePubSubMessage(inputToHTTPReq(input))
		errEqual := checkEqual(gotErr, nil)
		checkError(t, errEqual, "checkError: test: shouldBeValid\n")
	}

	var shouldBeInValid = []struct {
		input    reg.GCRPubSubPayload
		expected error
	}{
		{
			reg.GCRPubSubPayload{
				Action: "INSERT"},
			fmt.Errorf("gcrPayload: neither 'digest' nor 'tag' was specified"),
		},
		{
			reg.GCRPubSubPayload{
				FQIN: "gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000"},
			fmt.Errorf("gcrPayload: Action not specified"),
		},
		{
			reg.GCRPubSubPayload{
				Action: "DELETE",
				FQIN:   "gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000"},
			fmt.Errorf("{DELETE gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000    }: deletions are prohibited"),
		},
		{
			reg.GCRPubSubPayload{
				Action: "WOOF",
				FQIN:   "gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000"},
			fmt.Errorf("gcrPayload: unknown action \"WOOF\""),
		},
	}

	for _, test := range shouldBeInValid {
		_, gotErr := ParsePubSubMessage(inputToHTTPReq(test.input))
		errEqual := checkEqual(gotErr, test.expected)
		checkError(t, errEqual, "checkError: test: shouldBeInValid\n")
	}
}

// nolint[gocyclo]
func TestAudit(t *testing.T) {
	// Regression test case for
	// https://github.com/kubernetes-sigs/k8s-container-image-promoter/issues/191.
	manifests1 := []reg.Manifest{
		{
			Registries: []reg.RegistryContext{
				{
					Name: "gcr.io/k8s-staging-kas-network-proxy",
					Src:  true,
				},
				{
					Name:           "us.gcr.io/k8s-artifacts-prod/kas-network-proxy",
					ServiceAccount: "foobar@google-containers.iam.gserviceaccount.com",
				},
			},

			Images: []reg.Image{
				{
					ImageName: "proxy-agent",
					Dmap: reg.DigestTags{
						"sha256:c419394f3fa40c32352be5a6ec5865270376d4351a3756bb1893be3f28fcba32": {"v0.0.8"},
					},
				},
			},
		},
	}

	readRepo1 := map[string]string{
		"gcr.io/k8s-staging-kas-network-proxy": `{
  "child": [
    "proxy-agent"
  ],
  "manifest": {},
  "name": "k8s-staging-kas-network-proxy",
  "tags": []
}`,
		"gcr.io/k8s-staging-kas-network-proxy/proxy-agent": `{
  "child": [],
  "manifest": {
    "sha256:43273b274ee48f7fd7fc09bc82e7e75ddc596ca219fd9b522b1701bebec6ceff": {
      "imageSizeBytes": "6843680",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "tag": [],
      "timeCreatedMs": "1583451840426",
      "timeUploadedMs": "1583475320110"
    },
    "sha256:7bcbdf4cb26400ac576b33718000f0b630290dcf6380be3f60e33e5ba0461d31": {
      "imageSizeBytes": "7367874",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "tag": [],
      "timeCreatedMs": "1583451717939",
      "timeUploadedMs": "1583475314214"
    },
    "sha256:8735603bbd7153b8bfc8d2460481282bb44e2e830e5b237738e5c3e2a58c8f45": {
      "imageSizeBytes": "7396163",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "tag": [],
      "timeCreatedMs": "1583451882087",
      "timeUploadedMs": "1583475321761"
    },
    "sha256:99bade313218f3e6e63fdeb87bcddbf3a134aaa9e45e633be5ee5e60ddaac667": {
      "imageSizeBytes": "6888230",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "tag": [],
      "timeCreatedMs": "1583451799250",
      "timeUploadedMs": "1583475318193"
    },
    "sha256:c1ccf44d6b6fe49fc8506f7571f4a988ad69eb00c7747cd2b307b5e5b125a1f1": {
      "imageSizeBytes": "6888983",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "tag": [],
      "timeCreatedMs": "1583451758583",
      "timeUploadedMs": "1583475316361"
    },
    "sha256:c419394f3fa40c32352be5a6ec5865270376d4351a3756bb1893be3f28fcba32": {
      "imageSizeBytes": "0",
      "layerId": "",
      "mediaType": "application/vnd.docker.distribution.manifest.list.v2+json",
      "tag": [
        "v0.0.8"
      ],
      "timeCreatedMs": "0",
      "timeUploadedMs": "1583475321879"
    }
  },
  "name": "k8s-staging-kas-network-proxy/proxy-agent",
  "tags": [
    "v0.0.8"
  ]
}`,
	}

	// This is the response for reading the manifest for the parent
	// image by digest.
	readManifestList1 := map[string]string{
		"gcr.io/k8s-staging-kas-network-proxy/proxy-agent@sha256:c419394f3fa40c32352be5a6ec5865270376d4351a3756bb1893be3f28fcba32": `{
   "schemaVersion": 2,
   "mediaType": "application/vnd.docker.distribution.manifest.list.v2+json",
   "manifests": [
      {
         "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
         "size": 528,
         "digest": "sha256:7bcbdf4cb26400ac576b33718000f0b630290dcf6380be3f60e33e5ba0461d31",
         "platform": {
            "architecture": "amd64",
            "os": "linux"
         }
      },
      {
         "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
         "size": 528,
         "digest": "sha256:c1ccf44d6b6fe49fc8506f7571f4a988ad69eb00c7747cd2b307b5e5b125a1f1",
         "platform": {
            "architecture": "arm",
            "os": "linux"
         }
      },
      {
         "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
         "size": 528,
         "digest": "sha256:99bade313218f3e6e63fdeb87bcddbf3a134aaa9e45e633be5ee5e60ddaac667",
         "platform": {
            "architecture": "arm64",
            "os": "linux"
         }
      },
      {
         "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
         "size": 528,
         "digest": "sha256:43273b274ee48f7fd7fc09bc82e7e75ddc596ca219fd9b522b1701bebec6ceff",
         "platform": {
            "architecture": "ppc64le",
            "os": "linux"
         }
      },
      {
         "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
         "size": 528,
         "digest": "sha256:8735603bbd7153b8bfc8d2460481282bb44e2e830e5b237738e5c3e2a58c8f45",
         "platform": {
            "architecture": "s390x",
            "os": "linux"
         }
      }
   ]
}`,
	}

	type expectedPatterns struct {
		report []string
		info   []string
		error  []string
		alert  []string
	}

	var shouldBeValid = []struct {
		name             string
		manifests        []reg.Manifest
		payload          reg.GCRPubSubPayload
		readRepo         map[string]string
		readManifestList map[string]string
		expectedPatterns expectedPatterns
	}{
		{
			"direct manifest (tagless image)",
			manifests1,
			reg.GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "us.gcr.io/k8s-artifacts-prod/kas-network-proxy/proxy-agent@sha256:c419394f3fa40c32352be5a6ec5865270376d4351a3756bb1893be3f28fcba32",
				PQIN:   "us.gcr.io/k8s-artifacts-prod/kas-network-proxy/proxy-agent:v0.0.8",
			},
			readRepo1,
			readManifestList1,
			expectedPatterns{
				report: nil,
				info:   []string{`TRANSACTION VERIFIED`, `proxy-agent:v0.0.8}: agrees with manifest`},
				error:  nil,
				alert:  nil,
			},
		},
		{
			"child manifest (tagless child image, digest not in promoter manifest, but parent image is in promoter manifest)",
			manifests1,
			reg.GCRPubSubPayload{
				Action: "INSERT",
				FQIN:   "us.gcr.io/k8s-artifacts-prod/kas-network-proxy/proxy-agent@sha256:8735603bbd7153b8bfc8d2460481282bb44e2e830e5b237738e5c3e2a58c8f45",
				PQIN:   "",
			},
			readRepo1,
			readManifestList1,
			expectedPatterns{
				report: nil,
				info:   []string{`TRANSACTION VERIFIED`},
				error:  nil,
				alert:  nil,
			},
		},
	}

	for _, test := range shouldBeValid {

		// Create a new ResponseRecorder to record the response from Audit().
		w := httptest.NewRecorder()

		// Create a new Request to pass to the handler, which incorporates the
		// GCRPubSubPayload.
		payload, err := json.Marshal(test.payload)
		checkError(t, err, "checkError: test: shouldBeValid (payload)\n")

		psm := PubSubMessage{
			Message: PubSubMessageInner{
				Data: payload,
				ID:   "1"},
			Subscription: "2"}
		b, err := json.Marshal(psm)
		checkError(t, err, "checkError: test: shouldBeValid (psm)\n")

		r, err := http.NewRequest("POST", "/", bytes.NewBuffer(b))
		checkError(t, err, "checkError: test: shouldBeValid (NewRequest)\n")

		// test is used to pin the "test" variable from the outer "range" scope
		// (see scopelint) into the fakeReadRepo (in a sense it ensures that
		// fakeReadRepo closes over "test" in the outer scope, as a closure
		// should).
		test := test
		fakeReadRepo := func(sc *reg.SyncContext, rc reg.RegistryContext) stream.Producer {
			var sr stream.Fake

			_, domain, repoPath := reg.GetTokenKeyDomainRepoPath(rc.Name)
			key := fmt.Sprintf("%s/%s", domain, repoPath)
			fakeHTTPBody, ok := test.readRepo[key]
			if !ok {
				checkError(
					t,
					fmt.Errorf("could not read fakeHTTPBody"),
					fmt.Sprintf("Test: %v\n", test.name))
			}
			sr.Bytes = []byte(fakeHTTPBody)
			return &sr
		}

		fakeReadManifestList := func(sc *reg.SyncContext, gmlc reg.GCRManifestListContext) stream.Producer {
			var sr stream.Fake

			_, domain, repoPath := reg.GetTokenKeyDomainRepoPath(gmlc.RegistryContext.Name)
			key := fmt.Sprintf("%s/%s/%s@%s",
				domain,
				repoPath,
				gmlc.ImageName,
				gmlc.Digest)
			fakeHTTPBody, ok := test.readManifestList[key]
			if !ok {
				checkError(
					t,
					fmt.Errorf("could not read fakeHTTPBody"),
					fmt.Sprintf("Test: %v\n", test.name))
			}
			sr.Bytes = []byte(fakeHTTPBody)
			return &sr
		}

		reportingFacility := report.NewFakeReportingClient()
		loggingFacility := logclient.NewFakeLogClient()

		s := initFakeServerContext(
			test.manifests,
			reportingFacility,
			loggingFacility,
			fakeReadRepo,
			fakeReadManifestList)

		// Handle the request.
		s.Audit(w, r)

		// Check what happened!
		if status := w.Code; status != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v",
				status, http.StatusOK)
		}

		// Check all buffers for how the output should look like.
		reportBuffer := reportingFacility.GetReportBuffer()
		infoLogBuffer := loggingFacility.GetInfoBuffer()
		errorLogBuffer := loggingFacility.GetErrorBuffer()
		alertLogBuffer := loggingFacility.GetAlertBuffer()

		if len(test.expectedPatterns.report) > 0 {
			for _, pattern := range test.expectedPatterns.report {
				re := regexp.MustCompile(pattern)
				err := checkMatch(reportBuffer.Bytes(), re)
				checkError(t, err, fmt.Sprintf("test: %s (reportBuffer)\n", test.name))
			}
		} else {
			errEqual := checkEqual(reportBuffer.String(), "")
			checkError(t, errEqual, fmt.Sprintf("test: %s (reportBuffer)\n", test.name))
		}

		if len(test.expectedPatterns.info) > 0 {
			for _, pattern := range test.expectedPatterns.info {
				re := regexp.MustCompile(pattern)
				err := checkMatch(infoLogBuffer.Bytes(), re)
				checkError(t, err, fmt.Sprintf("test: %s (infoLogBuffer)\n", test.name))
			}
		} else {
			errEqual := checkEqual(infoLogBuffer.String(), "")
			checkError(t, errEqual, fmt.Sprintf("test: %s (infoLogBuffer)\n", test.name))
		}

		if len(test.expectedPatterns.error) > 0 {
			for _, pattern := range test.expectedPatterns.error {
				re := regexp.MustCompile(pattern)
				err := checkMatch(errorLogBuffer.Bytes(), re)
				checkError(t, err, fmt.Sprintf("test: %s (errorLogBuffer)\n", test.name))
			}
		} else {
			errEqual := checkEqual(errorLogBuffer.String(), "")
			checkError(t, errEqual, fmt.Sprintf("test: %s (errorLogBuffer)\n", test.name))
		}

		if len(test.expectedPatterns.alert) > 0 {
			for _, pattern := range test.expectedPatterns.alert {
				re := regexp.MustCompile(pattern)
				err := checkMatch(alertLogBuffer.Bytes(), re)
				checkError(t, err, fmt.Sprintf("test: %s (alertLogBuffer)\n", test.name))
			}
		} else {
			errEqual := checkEqual(alertLogBuffer.String(), "")
			checkError(t, errEqual, fmt.Sprintf("test: %s (alertLogBuffer)\n", test.name))
		}
	}
}

func initFakeServerContext(
	manifests []reg.Manifest,
	reportingFacility report.ReportingFacility,
	loggingFacility logclient.LoggingFacility,
	fakeReadRepo func(*reg.SyncContext, reg.RegistryContext) stream.Producer,
	fakeReadManifestList func(*reg.SyncContext, reg.GCRManifestListContext) stream.Producer,
) ServerContext {

	remoteManifestFacility := remotemanifest.NewFake(manifests)

	serverContext := ServerContext{
		ID:                     "cafec0ffee",
		RemoteManifestFacility: remoteManifestFacility,
		ErrorReportingFacility: reportingFacility,
		LoggingFacility:        loggingFacility,
		GcrReadingFacility: GcrReadingFacility{
			ReadRepo:         fakeReadRepo,
			ReadManifestList: fakeReadManifestList,
		},
	}

	return serverContext
}
