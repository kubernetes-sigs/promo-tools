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
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

// retryBackoff defines the exponential backoff for transient registry errors.
// With Duration=30s, Factor=2, Steps=6, Cap=5m the retry delays are roughly:
// 30s, 60s, 120s, 240s, 300s (capped) for a total budget of ~12.5 minutes.
// This is generous enough to outlast per-minute Artifact Registry quotas even
// when 80 concurrent workers compete for the same quota.
var retryBackoff = wait.Backoff{
	Duration: 30 * time.Second,
	Factor:   2,
	Jitter:   0.1,
	Steps:    6,
	Cap:      5 * time.Minute,
}

// IsTransient returns true for errors that indicate a temporary failure
// worth retrying: context deadline exceeded (per-request timeouts),
// HTTP 429 Too Many Requests, and 5xx server errors.
func IsTransient(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

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
