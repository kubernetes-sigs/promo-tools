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

package stream

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// Checks is from https://stackoverflow.com/a/40398093/437583.
func Checks(fs ...func() error) {
	for i := len(fs) - 1; i >= 0; i-- {
		if err := fs[i](); err != nil {
			fmt.Println("Received error:", err)
		}
	}
}

// HTTP is a wrapper around the net/http's Request type.
type HTTP struct {
	Req *http.Request
	Res *http.Response
}

// Produce runs the external process and returns two io.Readers (to stdout and
// stderr). In this case we equate the http.Respose "Body" with stdout.
func (h *HTTP) Produce() (io.Reader, io.Reader, error) {
	client := http.Client{
		Timeout: time.Second * 3, // 3-second timeout
	}

	res, err := client.Do(h.Req)
	if err != nil {
		return nil, nil, err
	}

	h.Res = res

	return res.Body, nil, nil
}

// Close closes the http request. This is required because otherwise there will
// be a resource leak.
// See https://stackoverflow.com/a/33238755/437583
func (h *HTTP) Close() error {
	return h.Res.Body.Close()
}
