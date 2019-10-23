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
	"reflect"
	"strings"
	"testing"

	reg "sigs.k8s.io/k8s-container-image-promoter/lib/dockerregistry"
)

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
			Digest: "gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			Action: "INSERT",
			Digest: "gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000",
			Tag:    "gcr.io/foo/bar:1.0",
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
			fmt.Errorf("gcrPayload: neither Digest nor Tag was specified"),
		},
		{
			reg.GCRPubSubPayload{
				Digest: "gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000"},
			fmt.Errorf("gcrPayload: Action not specified"),
		},
		{
			reg.GCRPubSubPayload{
				Action: "DELETE",
				Digest: "gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000"},
			fmt.Errorf("TRANSACTION REJECTED: {DELETE gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000 }: deletions are prohibited"),
		},
		{
			reg.GCRPubSubPayload{
				Action: "WOOF",
				Digest: "gcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000"},
			fmt.Errorf("gcrPayload: unknown action \"WOOF\""),
		},
	}

	for _, test := range shouldBeInValid {
		_, gotErr := ParsePubSubMessage(inputToHTTPReq(test.input))
		errEqual := checkEqual(gotErr, test.expected)
		checkError(t, errEqual, "checkError: test: shouldBeInValid\n")
	}
}
