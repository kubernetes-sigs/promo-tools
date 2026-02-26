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

// Package pipeline provides a generic pipeline engine for orchestrating
// promotion workflows. Each pipeline consists of ordered phases that
// execute sequentially, sharing state through the caller's closures.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// ErrStopPipeline is a sentinel error that a phase can return to cleanly
// stop the pipeline without indicating a failure. The pipeline's Run method
// returns nil when this error is encountered.
var ErrStopPipeline = errors.New("pipeline stopped")

// Phase is a single step in the promotion pipeline.
type Phase interface {
	// Name returns a human-readable name for the phase (e.g., "plan", "promote").
	Name() string

	// Run executes the phase. It should return an error if the phase fails.
	Run(ctx context.Context) error
}

// PhaseFunc adapts an ordinary function into a Phase.
type PhaseFunc struct {
	name string
	fn   func(ctx context.Context) error
}

// NewPhase creates a Phase from a name and function.
func NewPhase(name string, fn func(ctx context.Context) error) *PhaseFunc {
	return &PhaseFunc{name: name, fn: fn}
}

// Name returns the phase name.
func (p *PhaseFunc) Name() string { return p.name }

// Run executes the phase function.
func (p *PhaseFunc) Run(ctx context.Context) error { return p.fn(ctx) }

// Pipeline orchestrates a sequence of phases.
type Pipeline struct {
	phases []Phase
}

// New creates an empty pipeline.
func New() *Pipeline {
	return &Pipeline{}
}

// AddPhase appends a phase to the pipeline and returns the pipeline
// for chaining.
func (p *Pipeline) AddPhase(phase Phase) *Pipeline {
	p.phases = append(p.phases, phase)

	return p
}

// Run executes all phases in order. If any phase fails, execution
// stops and the error is returned.
func (p *Pipeline) Run(ctx context.Context) error {
	logrus.Infof("Pipeline starting with %d phases", len(p.phases))

	start := time.Now()

	for i, phase := range p.phases {
		select {
		case <-ctx.Done():
			return fmt.Errorf("pipeline cancelled: %w", ctx.Err())
		default:
		}

		logrus.Infof("Phase %d/%d: %s", i+1, len(p.phases), phase.Name())

		phaseStart := time.Now()

		if err := phase.Run(ctx); err != nil {
			if errors.Is(err, ErrStopPipeline) {
				logrus.Infof("Phase %q requested pipeline stop", phase.Name())

				return nil
			}

			return fmt.Errorf("phase %q failed: %w", phase.Name(), err)
		}

		logrus.Infof("Phase %q completed in %s", phase.Name(), time.Since(phaseStart))
	}

	logrus.Infof("Pipeline completed in %s", time.Since(start))

	return nil
}
