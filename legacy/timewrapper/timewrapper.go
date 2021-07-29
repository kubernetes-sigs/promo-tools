/*
Copyright 2021 The Kubernetes Authors.

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

package timewrapper

import (
	"time"
)

// Time groups the functions mocked by FakeTime.
type Time interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// RealTime is a wrapper for actual time functions.
type RealTime struct{}

// Now simply calls time.Now()
func (RealTime) Now() time.Time { return time.Now() }

// Sleep simply calls time.Sleep(d), using the given duration.
func (RealTime) Sleep(d time.Duration) { time.Sleep(d) }

// FakeTime is an advancing clock, supporting only a single sleeping goroutine.
type FakeTime struct {
	Time      time.Time
	Interval  time.Duration
	CheckTime chan bool
	Updated   chan bool
}

// NewFakeTime creates a new fake time with the provided start time.
func NewFakeTime(startTime time.Time) FakeTime {
	return FakeTime{
		Time:      startTime,
		CheckTime: make(chan bool),
		Updated:   make(chan bool),
	}
}

// Advance adds the duration to the global fake time by braking the
// duration up into a series of time-steps. Each time-step is either less than
// or equal to an observed sleep interval. This allows multiple sleep/wake cycles
// to occur on time advancements that are orders longer than the given sleep interval.
func (ft *FakeTime) Advance(d time.Duration) {
	// Determine the number of time steps we must make.
	var timeStep, timePast time.Duration
	timePast = 0
	for timePast < d {
		timeStep = ft.Interval
		if timePast+timeStep > d {
			timeStep = d - timePast
		}
		// Make the time step.
		ft.step(timeStep)
		timePast += timeStep
	}
}

// step increments time by the given duration. This duration should either be less than
// or equal to the observed sleep interval.
func (ft *FakeTime) step(d time.Duration) {
	if d > ft.Interval {
		panic("timewrapper.step received a duration greater than the observed time interval.")
	}
	ft.Time = ft.Time.Add(d)
	// Notify the sleeping goroutine to check the current time.
	ft.CheckTime <- true
	// Wait for the sleeping goroutine to respond.
	ft.WaitForSleeper()
}

// Now returns the global fake time.
func (ft *FakeTime) Now() time.Time { return ft.Time }

// WaitForSleeper blocks until a goroutine has notified that it's up to date
// with the current global fake time.
func (ft *FakeTime) WaitForSleeper() {
	<-ft.Updated
}

// Sleep does not block the current goroutine.
func (ft *FakeTime) Sleep(d time.Duration) {
	// Record the sleep interval for the Advance function to break up
	// time advancement into a series of time-steps.
	ft.Interval = d
	// Determine when to wake up from sleep.
	wakeAt := ft.Time.Add(d)
	// Notify the goroutine is sleeping.
	ft.Updated <- true
	for {
		// Blocking the current goroutine.
		<-ft.CheckTime
		if wakeAt.After(ft.Time) {
			// Notify the goroutine has seen the fake time update.
			ft.Updated <- true
		} else {
			return
		}
	}
}
