/*
Copyright 2023 The Kubernetes Authors.

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
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

const (
	// DefaultBurst allows small bursts while still respecting the per-second
	// limit. Increased from 1 to reduce starvation under concurrent load.
	DefaultBurst = 5

	// MaxEvents is the target request rate for Artifact Registry.
	// https://github.com/kubernetes/registry.k8s.io/issues/153#issuecomment-1460913153
	// AR limit is ~83 req/sec (83*60=4980/min). We use 50 as a conservative
	// target to leave headroom for retries and other API consumers.
	MaxEvents rate.Limit = 50

	// backoffDuration is how long to pause after receiving a 429 response.
	backoffDuration = 10 * time.Second

	// backoffCooldown is the minimum interval between backoff events to
	// prevent repeated 429s from causing excessive pausing.
	backoffCooldown = 15 * time.Second
)

// RoundTripper wraps an http.RoundTripper with rate limiting and adaptive
// backoff on 429 responses.
type RoundTripper struct {
	name         string
	rateLimiter  *rate.Limiter
	roundTripper http.RoundTripper

	mu            sync.Mutex
	lastBackoff   time.Time
	backoffUntil  time.Time
	totalWaited   time.Duration
	totalRequests int64
}

var _ http.RoundTripper = &RoundTripper{}

// Limiter is the global default rate limiter, kept for backwards compatibility.
//
// Deprecated: Use NewRoundTripper or a BudgetAllocator instead of the global
// singleton. The global singleton causes promotion and signing to compete for
// the same rate budget, leading to 429 errors under load.
var Limiter *RoundTripper

func init() {
	if Limiter == nil {
		Limiter = NewRoundTripper(MaxEvents)
	}
}

// NewRoundTripper creates a rate-limited HTTP transport with the given
// requests-per-second limit.
func NewRoundTripper(limit rate.Limit) *RoundTripper {
	return NewNamedRoundTripper("default", limit, DefaultBurst)
}

// NewNamedRoundTripper creates a named rate-limited HTTP transport for
// observability.
func NewNamedRoundTripper(name string, limit rate.Limit, burst int) *RoundTripper {
	return &RoundTripper{
		name:         name,
		rateLimiter:  rate.NewLimiter(limit, burst),
		roundTripper: http.DefaultTransport,
	}
}

// RoundTrip executes the HTTP request with rate limiting. All HTTP methods are
// rate-limited because Artifact Registry quotas apply to both reads and writes.
// If a 429 response is received, an adaptive backoff pauses future requests.
func (rt *RoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	ctx := r.Context()

	// Wait for any active backoff to expire.
	rt.waitForBackoff(ctx)

	// Wait for rate limiter token.
	if err := rt.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	rt.mu.Lock()
	rt.totalRequests++
	rt.mu.Unlock()

	resp, err := rt.roundTripper.RoundTrip(r)
	if err != nil {
		return resp, err
	}

	// Adaptive backoff: if we get a 429, temporarily pause all requests
	// through this limiter to let the quota recover.
	if resp.StatusCode == http.StatusTooManyRequests {
		rt.triggerBackoff()
	}

	return resp, nil
}

// SetLimit changes the rate limit dynamically. This is used by BudgetAllocator
// to rebalance budgets between phases.
func (rt *RoundTripper) SetLimit(newLimit rate.Limit) {
	rt.rateLimiter.SetLimit(newLimit)
	logrus.WithField("limiter", rt.name).Infof(
		"Rate limit adjusted to %.1f req/sec", float64(newLimit),
	)
}

// SetBurst changes the burst limit dynamically.
func (rt *RoundTripper) SetBurst(newBurst int) {
	rt.rateLimiter.SetBurst(newBurst)
}

// Stats returns observability data about this rate limiter.
func (rt *RoundTripper) Stats() (totalRequests int64, totalWaited time.Duration) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.totalRequests, rt.totalWaited
}

// Name returns the name of this rate limiter.
func (rt *RoundTripper) Name() string {
	return rt.name
}

func (rt *RoundTripper) waitForBackoff(ctx context.Context) {
	rt.mu.Lock()
	until := rt.backoffUntil
	rt.mu.Unlock()

	if until.IsZero() || time.Now().After(until) {
		return
	}

	wait := time.Until(until)
	logrus.WithField("limiter", rt.name).Warnf(
		"Backoff active, waiting %s before next request", wait.Round(time.Millisecond),
	)

	select {
	case <-time.After(wait):
	case <-ctx.Done():
	}

	rt.mu.Lock()
	rt.totalWaited += wait
	rt.mu.Unlock()
}

func (rt *RoundTripper) triggerBackoff() {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	now := time.Now()
	if now.Sub(rt.lastBackoff) < backoffCooldown {
		return
	}

	rt.lastBackoff = now
	rt.backoffUntil = now.Add(backoffDuration)
	logrus.WithField("limiter", rt.name).Warnf(
		"Received 429 Too Many Requests, backing off for %s", backoffDuration,
	)
}
