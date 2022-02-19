/*
Copyright 2019 The Kubernetes Authors.

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

package stream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/authn/github"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// HTTP is a wrapper around the net/http's Request type.
type HTTP struct {
	Req *http.Request
	Res *http.Response
}

const (
	requestTimeoutSeconds = 3
)

var keychain = authn.NewMultiKeychain(
	authn.DefaultKeychain,
	google.Keychain,
	github.Keychain,
)

// Produce runs the external process and returns two io.Readers (to stdout and
// stderr). In this case we equate the http.Respose "Body" with stdout.
func (h *HTTP) Produce() (stdOut, stdErr io.Reader, err error) {
	client := http.Client{
		Timeout: time.Second * requestTimeoutSeconds,
	}

	// If we already have an authentication scheme it means that we have a GCP
	// service account token set. Otherwise try to get it from the local config
	// using the keychains from GGCR
	if h.Req.Header.Get("Authorization") == "" {
		// GCR uses oauth tokens but the promoter makes direct HTTP calls
		// to the registry URLs. If we try to generate the oauth auth tokens
		// for paths under under /v2/, it will fail because we need them scoped
		// to the repo at project+image
		repoPath := h.Req.URL.Path
		if strings.HasSuffix(h.Req.URL.Host, "gcr.io") {
			parts := strings.Split(repoPath, "/")
			if len(parts) > 2 {
				repoPath = string(filepath.Separator) + filepath.Join(parts[2:]...)
			}
		}
		repo, err := name.NewRepository(h.Req.URL.Host + repoPath)

		logrus.Debugf("Making request to %s", h.Req.URL.Host+h.Req.URL.Path)
		logrus.Debugf("... with creds for %s", h.Req.URL.Host+repoPath)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "getting repo from %s", h.Req.URL.String())
		}

		auth, err := keychain.Resolve(repo.Registry)
		if err != nil {
			return nil, nil, errors.Wrap(err, "resolving registry authorization")
		}

		a, err := auth.Authorization()
		if err != nil {
			return nil, nil, errors.Wrap(err, "creating authorization")
		}
		logrus.Debugf("Resolved auth info: Username: %v Password: %v Token: %v",
			len(a.Username) > 0, len(a.Password) > 0, len(a.Auth) > 0,
		)

		t, err := transport.NewWithContext(
			context.Background(), repo.Registry, auth, http.DefaultTransport,
			[]string{repo.Scope(transport.PushScope)},
		)
		if err != nil {
			return nil, nil, errors.Wrap(err, "creating transport")
		}

		client.Transport = t
	}

	// TODO: Does Close() need to be handled in a separate method?
	// We close the response body in Close().
	//nolint:bodyclose
	h.Res, err = client.Do(h.Req)

	if err != nil {
		return nil, nil, errors.Wrap(err, "sending request")
	}

	if h.Res.StatusCode == http.StatusOK {
		// If debugging, log the output if the request
		if logrus.StandardLogger().Level == logrus.DebugLevel {
			body, err := io.ReadAll(h.Res.Body)
			if err != nil {
				return nil, nil, errors.Wrap(err, "reading http response body")
			}
			logrus.Debug("Response body: " + string(body))
			return io.NopCloser(bytes.NewReader(body)), nil, nil
		}
		return h.Res.Body, nil, nil
	}

	// Try to glean some additional information by reading from the response
	// body.
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(h.Res.Body)
	if err != nil {
		logrus.Errorf("could not read from HTTP response body")
		return nil, nil, fmt.Errorf(
			"problems encountered: unexpected response code %d",
			h.Res.StatusCode,
		)
	}

	return nil, nil, fmt.Errorf(
		"problems encountered: unexpected response code %d; body: %s",
		h.Res.StatusCode,
		buf.String(),
	)
}

// Close closes the http request. This is required because otherwise there will
// be a resource leak.
// See https://stackoverflow.com/a/33238755/437583
func (h *HTTP) Close() error {
	return h.Res.Body.Close()
}
