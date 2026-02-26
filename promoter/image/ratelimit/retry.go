/*
Copyright 2026 The Kubernetes Authors.

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

package ratelimit

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

// retryBackoff defines the exponential backoff for transient registry errors.
var retryBackoff = wait.Backoff{
	Duration: 30 * time.Second,
	Factor:   2,
	Jitter:   0.1,
	Steps:    3,
}

// IsTransient returns true for HTTP status codes that indicate a temporary
// failure worth retrying (429 Too Many Requests and 5xx server errors).
func IsTransient(err error) bool {
	var terr *transport.Error
	if errors.As(err, &terr) {
		return terr.StatusCode == http.StatusTooManyRequests ||
			terr.StatusCode >= http.StatusInternalServerError
	}

	return false
}

// WithRetry calls fn with exponential backoff on transient registry errors.
// Non-transient errors (including 404 Not Found) are returned immediately.
func WithRetry(fn func() error) error {
	var lastErr error

	err := wait.ExponentialBackoff(retryBackoff, func() (bool, error) {
		lastErr = fn()
		if lastErr == nil {
			return true, nil // success, stop retrying
		}

		if !IsTransient(lastErr) {
			return false, lastErr // permanent error, stop retrying
		}

		logrus.Warnf("Transient error (will retry): %v", lastErr)

		return false, nil // transient error, keep retrying
	})
	if wait.Interrupted(err) {
		return lastErr // retries exhausted, return the last transient error
	}

	if err != nil {
		return fmt.Errorf("exponential backoff: %w", err)
	}

	return nil
}
