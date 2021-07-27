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
	"sync"
	"time"
)

// event holds the information to unblock sleeping processes. Calls to Advance() will push a new event
// with the updated time to all event channels. Processes blocked by Sleep() will receive these events
// and unblock when their sleep duration has expired.
type event struct {
	time   time.Time // current global time.
	cancel bool      // determine whether the sleeping process should cancel sleep.
}

const (
	// DefaultTime is used to initialize the global time.
	// This represents September 22, 2002 at 00:00:00.
	DefaultTime string = "020106 000000"
)

var (
	// GlobalTime is the primary source of truth for measuring "fake" time.
	GlobalTime time.Time
	// mutex is used to prevent race conditions when reading or writing any global variable.
	mutex sync.Mutex
	// events holds all event channels for sleeping processes.
	events []chan event
	// SleepGroup is used to ensure all Sleep() processes are blocked and listening to their event channel.
	SleepGroup sync.WaitGroup
	// eventGroup is used after a call to Advance(), ensuring all sleeping processes have received their event.
	eventGroup sync.WaitGroup
)

// Init defines the starting state to mock the time.Sleep function.
func Init() {
	var err error
	GlobalTime, err = time.Parse(DefaultTime, DefaultTime)
	if err != nil {
		msg := fmt.Sprintf("The default time %s could not be parsed in fake_time.Init()", DefaultTime)
		panic(msg)
	}
	mutex = sync.Mutex{}
	events = make([]chan event, 0)
	SleepGroup = sync.WaitGroup{}
	eventGroup = sync.WaitGroup{}
}

// Close cancels and closes all event channels, effectively unblocking all sleeping processes.
func Close() {
	for _, e := range events {
		eventGroup.Add(1)
		e <- event{time: GlobalTime, cancel: true}
		close(e)
	}
	eventGroup.Wait()
}

// Advance adds the given duration to the global time, and pushes the new time to listeners
// of the event channel. This advance in time MUST trickle down to all sleeping processes in order
// to have a global consensus of time. Therefore, we must wait until every sleeping process has
// received their event.
func Advance(d time.Duration) {
	// Grab and hold the lock to prevent race conditions.
	mutex.Lock()
	defer mutex.Unlock()
	// Advance the global time.
	GlobalTime = GlobalTime.Add(d)
	// Update all sleeping processes of the new current time.
	for _, e := range events {
		eventGroup.Add(1)
		e <- event{time: GlobalTime, cancel: false}
	}
	// Block execution until all sleeping processes have received the event.
	eventGroup.Wait()
}

// Sleep creates a new event channel and blocks until the global time surpasses the given duration.
func Sleep(d time.Duration) {
	var ec chan event
	var unblockAt time.Time

	mutex.Lock()
	// Create a new event channel for the sleeping process.
	ec = make(chan event)
	events = append(events, ec)
	// Determine when to stop sleeping.
	unblockAt = GlobalTime.Add(d)
	mutex.Unlock()

	// Acknowledge this sleeping process has finished initializing, and is now listening to
	// its event channel. This must be done to synchronize all sleeping processes before any
	// call to Advance() is made.
	SleepGroup.Done()
	// Block until it's time to wake up.
	for {
		if wakeUp(unblockAt, ec) {
			removeEventChannel(ec)
			return
		}
	}
}

// removeEventChannel removes the given event channel from the global collection of channels.
func removeEventChannel(ec chan event) {
	for i := 0; i < len(events); i++ {
		if events[i] == ec {
			events = append(events[:i], events[i+1:]...)
		}
	}
}

// wakeUp only returns true if the given event channel has closed or the event time has surpassed
// the given time.
func wakeUp(alarm time.Time, ec chan event) bool {
	// Acknowledge this sleeping process has seen the new event upon return.
	defer eventGroup.Done()
	// Wait for a new event.
	event := <-ec
	return (event.cancel || event.time.After(alarm))
}
