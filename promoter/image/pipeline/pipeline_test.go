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

package pipeline

import (
	"context"
	"errors"
	"testing"
)

func TestPipelineEmpty(t *testing.T) {
	p := New()
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("empty pipeline should succeed, got: %v", err)
	}
}

func TestPipelineExecutesInOrder(t *testing.T) {
	var order []string

	p := New()
	p.AddPhase(NewPhase("first", func(_ context.Context) error {
		order = append(order, "first")
		return nil
	}))
	p.AddPhase(NewPhase("second", func(_ context.Context) error {
		order = append(order, "second")
		return nil
	}))
	p.AddPhase(NewPhase("third", func(_ context.Context) error {
		order = append(order, "third")
		return nil
	}))

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 phases, got %d", len(order))
	}
	for i, want := range []string{"first", "second", "third"} {
		if order[i] != want {
			t.Errorf("order[%d] = %q, want %q", i, order[i], want)
		}
	}
}

func TestPipelineStopsOnError(t *testing.T) {
	var executed []string

	p := New()
	p.AddPhase(NewPhase("ok", func(_ context.Context) error {
		executed = append(executed, "ok")
		return nil
	}))
	p.AddPhase(NewPhase("fail", func(_ context.Context) error {
		executed = append(executed, "fail")
		return errors.New("boom")
	}))
	p.AddPhase(NewPhase("skipped", func(_ context.Context) error {
		executed = append(executed, "skipped")
		return nil
	}))

	err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if len(executed) != 2 {
		t.Fatalf("expected 2 phases executed, got %d: %v", len(executed), executed)
	}
	if executed[0] != "ok" || executed[1] != "fail" {
		t.Errorf("unexpected execution order: %v", executed)
	}
}

func TestPipelineCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	p := New()
	p.AddPhase(NewPhase("should-not-run", func(_ context.Context) error {
		t.Error("phase should not have run")
		return nil
	}))

	err := p.Run(ctx)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestPipelineChaining(t *testing.T) {
	p := New().
		AddPhase(NewPhase("a", func(_ context.Context) error { return nil })).
		AddPhase(NewPhase("b", func(_ context.Context) error { return nil }))

	if len(p.phases) != 2 {
		t.Errorf("expected 2 phases, got %d", len(p.phases))
	}
}

func TestPhaseFuncName(t *testing.T) {
	p := NewPhase("test-phase", func(_ context.Context) error { return nil })
	if p.Name() != "test-phase" {
		t.Errorf("Name() = %q, want %q", p.Name(), "test-phase")
	}
}

func TestPipelinePassesContext(t *testing.T) {
	type ctxKey string
	key := ctxKey("test")

	p := New()
	p.AddPhase(NewPhase("check-ctx", func(ctx context.Context) error {
		val := ctx.Value(key)
		if val != "hello" {
			t.Errorf("context value = %v, want %q", val, "hello")
		}
		return nil
	}))

	ctx := context.WithValue(context.Background(), key, "hello")
	if err := p.Run(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPipelineErrStopPipeline(t *testing.T) {
	var executed []string

	p := New()
	p.AddPhase(NewPhase("runs", func(_ context.Context) error {
		executed = append(executed, "runs")
		return nil
	}))
	p.AddPhase(NewPhase("stops", func(_ context.Context) error {
		executed = append(executed, "stops")
		return ErrStopPipeline
	}))
	p.AddPhase(NewPhase("skipped", func(_ context.Context) error {
		executed = append(executed, "skipped")
		return nil
	}))

	err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("ErrStopPipeline should result in nil error, got: %v", err)
	}
	if len(executed) != 2 {
		t.Fatalf("expected 2 phases, got %d: %v", len(executed), executed)
	}
	if executed[0] != "runs" || executed[1] != "stops" {
		t.Errorf("unexpected order: %v", executed)
	}
}

func TestPipelineSharedState(t *testing.T) {
	// Verify that phases can share state through closures.
	var result int

	p := New()
	p.AddPhase(NewPhase("produce", func(_ context.Context) error {
		result = 42
		return nil
	}))
	p.AddPhase(NewPhase("consume", func(_ context.Context) error {
		if result != 42 {
			t.Errorf("shared state = %d, want 42", result)
		}
		return nil
	}))

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
