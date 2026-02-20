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
	"math"
	"testing"

	"golang.org/x/time/rate"
)

func TestBudgetAllocatorAllocate(t *testing.T) {
	ba := NewBudgetAllocator(100)

	promo := ba.Allocate("promotion", 0.7)
	sign := ba.Allocate("signing", 0.3)

	if promo == nil || sign == nil {
		t.Fatal("Allocate returned nil")
	}

	// Verify rates are correctly partitioned.
	promoLimit := promo.rateLimiter.Limit()
	signLimit := sign.rateLimiter.Limit()

	if math.Abs(float64(promoLimit)-70.0) > 0.1 {
		t.Errorf("expected promotion limit ~70, got %v", promoLimit)
	}
	if math.Abs(float64(signLimit)-30.0) > 0.1 {
		t.Errorf("expected signing limit ~30, got %v", signLimit)
	}
}

func TestBudgetAllocatorGet(t *testing.T) {
	ba := NewBudgetAllocator(100)
	ba.Allocate("test", 0.5)

	rt, err := ba.Get("test")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if rt.Name() != "test" {
		t.Errorf("expected name 'test', got %q", rt.Name())
	}

	_, err = ba.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent allocation")
	}
}

func TestBudgetAllocatorRebalance(t *testing.T) {
	ba := NewBudgetAllocator(100)
	promo := ba.Allocate("promotion", 0.7)
	sign := ba.Allocate("signing", 0.3)

	// Rebalance: move 20% from promotion to signing.
	if err := ba.Rebalance("promotion", "signing", 0.2); err != nil {
		t.Fatalf("Rebalance failed: %v", err)
	}

	promoLimit := promo.rateLimiter.Limit()
	signLimit := sign.rateLimiter.Limit()

	if math.Abs(float64(promoLimit)-50.0) > 0.1 {
		t.Errorf("after rebalance, expected promotion limit ~50, got %v", promoLimit)
	}
	if math.Abs(float64(signLimit)-50.0) > 0.1 {
		t.Errorf("after rebalance, expected signing limit ~50, got %v", signLimit)
	}
}

func TestBudgetAllocatorRebalanceErrors(t *testing.T) {
	ba := NewBudgetAllocator(100)
	ba.Allocate("a", 0.5)

	if err := ba.Rebalance("nonexistent", "a", 0.1); err == nil {
		t.Error("expected error for nonexistent source")
	}
	if err := ba.Rebalance("a", "nonexistent", 0.1); err == nil {
		t.Error("expected error for nonexistent destination")
	}
}

func TestBudgetAllocatorGiveAll(t *testing.T) {
	ba := NewBudgetAllocator(100)
	promo := ba.Allocate("promotion", 0.7)
	sign := ba.Allocate("signing", 0.3)

	if err := ba.GiveAll("signing"); err != nil {
		t.Fatalf("GiveAll failed: %v", err)
	}

	promoLimit := promo.rateLimiter.Limit()
	signLimit := sign.rateLimiter.Limit()

	if promoLimit != rate.Limit(0) {
		t.Errorf("expected promotion limit 0 after GiveAll, got %v", promoLimit)
	}
	if math.Abs(float64(signLimit)-100.0) > 0.1 {
		t.Errorf("expected signing limit ~100 after GiveAll, got %v", signLimit)
	}
}

func TestBudgetAllocatorGiveAllError(t *testing.T) {
	ba := NewBudgetAllocator(100)
	if err := ba.GiveAll("nonexistent"); err == nil {
		t.Error("expected error for nonexistent allocation")
	}
}

func TestBudgetAllocatorRebalanceClampToZero(t *testing.T) {
	ba := NewBudgetAllocator(100)
	ba.Allocate("a", 0.3)
	ba.Allocate("b", 0.7)

	// Rebalance more than what 'a' has — should clamp to 0.
	if err := ba.Rebalance("a", "b", 0.5); err != nil {
		t.Fatalf("Rebalance failed: %v", err)
	}

	a, err := ba.Get("a")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if a.rateLimiter.Limit() != 0 {
		t.Errorf("expected clamped limit 0, got %v", a.rateLimiter.Limit())
	}
}

func TestBudgetAllocatorStats(t *testing.T) {
	ba := NewBudgetAllocator(100)
	ba.Allocate("a", 0.5)
	ba.Allocate("b", 0.5)

	stats := ba.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats entries, got %d", len(stats))
	}
	if _, ok := stats["a"]; !ok {
		t.Error("expected stats for 'a'")
	}
	if _, ok := stats["b"]; !ok {
		t.Error("expected stats for 'b'")
	}
}
