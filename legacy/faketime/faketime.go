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

package faketime

import (
	"fmt"
	"sync/atomic"
	"time"
)

type Time interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type RealTime struct{}

func (RealTime) Now() time.Time        { return time.Now() }
func (RealTime) Sleep(d time.Duration) { time.Sleep(d) }

type FakeTime struct {
	time int64 // The current global time measured in Nanoseconds.
}

// Advance adds the given duration to the global time.
func (ft *FakeTime) Advance(d time.Duration) {
	atomic.StoreInt64(&ft.time, d.Nanoseconds())
}

// Now returns the current global time.
func (ft *FakeTime) Now() time.Time {
	return time.Unix(0, ft.time)
}

// Sleep until the global time has met or exceeded the given duration.
func (ft *FakeTime) Sleep(d time.Duration) {
	fmt.Println("sleeping...")
	// Determine when to the goroutine should unblock.
	until := atomic.LoadInt64(&ft.time) + d.Nanoseconds()
	// Repeatedly check the global time,.
	for {
		if atomic.LoadInt64(&ft.time) >= until {
			return
		}
	}
}
