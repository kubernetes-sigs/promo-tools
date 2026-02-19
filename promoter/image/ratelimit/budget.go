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
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

// BudgetAllocator partitions a total requests-per-second budget into named
// sub-budgets. Each sub-budget gets its own RoundTripper with a fraction of
// the total rate. Budgets can be rebalanced at runtime, e.g., when promotion
// finishes and signing should get the full budget.
type BudgetAllocator struct {
	mu          sync.Mutex
	total       rate.Limit
	allocations map[string]*allocation
}

type allocation struct {
	limiter  *RoundTripper
	fraction float64
}

// NewBudgetAllocator creates a new allocator with the given total
// requests-per-second budget.
func NewBudgetAllocator(totalEventsPerSecond rate.Limit) *BudgetAllocator {
	return &BudgetAllocator{
		total:       totalEventsPerSecond,
		allocations: make(map[string]*allocation),
	}
}

// Allocate creates a named rate limiter with the given fraction of the total
// budget. The fraction must be between 0 and 1. The sum of all fractions
// should not exceed 1.0.
func (b *BudgetAllocator) Allocate(name string, fraction float64) *RoundTripper {
	b.mu.Lock()
	defer b.mu.Unlock()

	limit := rate.Limit(float64(b.total) * fraction)
	rt := NewNamedRoundTripper(name, limit, DefaultBurst)

	b.allocations[name] = &allocation{
		limiter:  rt,
		fraction: fraction,
	}

	logrus.WithField("allocator", "budget").Infof(
		"Allocated %q: %.1f req/sec (%.0f%% of %.1f total)",
		name, float64(limit), fraction*100, float64(b.total),
	)

	return rt
}

// Get returns the rate limiter for the given name, or an error if it doesn't
// exist.
func (b *BudgetAllocator) Get(name string) (*RoundTripper, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	alloc, ok := b.allocations[name]
	if !ok {
		return nil, fmt.Errorf("no rate limiter allocated with name %q", name)
	}
	return alloc.limiter, nil
}

// Rebalance shifts budget from one allocation to another. The amount is
// specified as a fraction of the total budget (0 to 1). The source
// allocation's rate is reduced and the destination's is increased.
func (b *BudgetAllocator) Rebalance(from, to string, fraction float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	fromAlloc, ok := b.allocations[from]
	if !ok {
		return fmt.Errorf("source allocation %q not found", from)
	}
	toAlloc, ok := b.allocations[to]
	if !ok {
		return fmt.Errorf("destination allocation %q not found", to)
	}

	fromAlloc.fraction -= fraction
	if fromAlloc.fraction < 0 {
		fromAlloc.fraction = 0
	}
	toAlloc.fraction += fraction

	fromAlloc.limiter.SetLimit(rate.Limit(float64(b.total) * fromAlloc.fraction))
	toAlloc.limiter.SetLimit(rate.Limit(float64(b.total) * toAlloc.fraction))

	logrus.WithField("allocator", "budget").Infof(
		"Rebalanced: %q=%.0f%%, %q=%.0f%%",
		from, fromAlloc.fraction*100, to, toAlloc.fraction*100,
	)

	return nil
}

// GiveAll transfers the entire budget to a single allocation. All other
// allocations are set to zero. This is useful when a phase completes and
// the remaining phase should get full throughput.
func (b *BudgetAllocator) GiveAll(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	target, ok := b.allocations[name]
	if !ok {
		return fmt.Errorf("allocation %q not found", name)
	}

	for n, alloc := range b.allocations {
		if n == name {
			alloc.fraction = 1.0
			alloc.limiter.SetLimit(b.total)
		} else {
			alloc.fraction = 0
			alloc.limiter.SetLimit(0)
		}
	}

	logrus.WithField("allocator", "budget").Infof(
		"Gave full budget to %q: %.1f req/sec",
		name, float64(target.limiter.rateLimiter.Limit()),
	)

	return nil
}

// Stats returns the total requests and wait time for all allocations.
func (b *BudgetAllocator) Stats() map[string]struct {
	Requests int64
	Waited   string
} {
	b.mu.Lock()
	defer b.mu.Unlock()

	stats := make(map[string]struct {
		Requests int64
		Waited   string
	})
	for name, alloc := range b.allocations {
		reqs, waited := alloc.limiter.Stats()
		stats[name] = struct {
			Requests int64
			Waited   string
		}{reqs, waited.String()}
	}
	return stats
}
