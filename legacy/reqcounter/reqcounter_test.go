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

package reqcounter_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	rc "sigs.k8s.io/k8s-container-image-promoter/legacy/reqcounter"
	tw "sigs.k8s.io/k8s-container-image-promoter/legacy/timewrapper"
)

// defaultTime should be used as a timestamp for all request counters.
// The actual time represents: September 22, 2002 at 05:03:16.
var defaultTime, _ = time.Parse("020106 150405", "020106 150405")

// NewRequestCounter returns a new request counter with the given number of requests.
// All other object fields are set to default values.
func NewRequestCounter(requests uint64) rc.RequestCounter {
	return rc.RequestCounter{
		Mutex:    sync.Mutex{},
		Requests: requests,
		Since:    defaultTime,
		Interval: time.Second,
	}
}

func TestInit(t *testing.T) {
	rc.Init()
	// Ensure request counting is enabled.
	require.True(t, rc.EnableCounting, "Init did not enable counting.")
	// Ensure request counters are created.
	require.Greater(t, len(rc.NetMonitor.RequestCounters), 0, "Init did not create any request counters within the global Monitor.")
	// Ensure at least one request counter uses the MeasurementWindow.
	found := false
	for _, requestCounter := range rc.NetMonitor.RequestCounters {
		if requestCounter.Interval == rc.MeasurementWindow {
			found = true
			break
		}
	}
	require.True(t, found, "No request counters are using the Interval: MeasurementWindow.")
}

func TestIncrement(t *testing.T) {
	// Create request counters which use these request counts and
	// populate the Monitor global variable.
	requestCounters := []rc.RequestCounter{
		NewRequestCounter(0),
		NewRequestCounter(9),
		NewRequestCounter(2839),
	}
	netMonitor := &rc.NetworkMonitor{
		RequestCounters: rc.RequestCounters{
			&requestCounters[0],
			&requestCounters[1],
			&requestCounters[2],
		},
	}
	// Create the request counters we expect to get after calling rc.Increment.
	expectedCounters := []rc.RequestCounter{
		NewRequestCounter(1),
		NewRequestCounter(10),
		NewRequestCounter(2840),
	}
	expectedNetMonitor := &rc.NetworkMonitor{
		RequestCounters: rc.RequestCounters{
			&expectedCounters[0],
			&expectedCounters[1],
			&expectedCounters[2],
		},
	}
	// Set the global network monitor.
	rc.NetMonitor = netMonitor
	// Ensure request counter modification can only occur when counting is enabled. Therefore,
	// the global network monitor should not be mutated with this call to Increment.
	rc.EnableCounting = false
	rc.Increment()
	require.EqualValues(t, netMonitor, rc.NetMonitor, "Request counters were modified while counting was disabled.")
	// Ensure the Increment function actually increments each request counter's requests field.
	rc.EnableCounting = true
	rc.Increment()
	require.EqualValues(t, expectedNetMonitor, rc.NetMonitor, "Request counters were not incremented correctly.")
}

func TestFlush(t *testing.T) {
	// Create a local invocation of time.
	requestCounter := NewRequestCounter(33)
	// Mock the logrus.Debug function.
	debugCalls := 0
	rc.Debug = func(args ...interface{}) {
		debugCalls++
	}
	requestCounter.Flush()
	// Ensure logrus.Debug was called.
	require.Equal(t, 1, debugCalls, "Flush() failed to trigger a debug statement.")
	// Ensure the request counter is reset, where time advances and the requests are zeroed.
	require.Equal(t, uint64(0), requestCounter.Requests, "Calling Flush() did not reset the request counter to 0.")
	require.True(t, defaultTime.Before(requestCounter.Since), "Calling Flush() did not reset the request counter timestamp.")
}

func TestRequestCounterIncrement(t *testing.T) {
	// Create a simple request counter.
	requestCounter := NewRequestCounter(36)
	// Create a request counter expected after Increment is called.
	expected := NewRequestCounter(37)
	// Increment the counter.
	requestCounter.Increment()
	// Ensure the request counter was incremented.
	require.EqualValues(t, &expected, &requestCounter, "The request counter failed to increment its request field.")
}

func TestLog(t *testing.T) {
	// Create multiple request counters with unique number of starting requests. These unique requests will
	// be the way to differentiate between request counter log statements.
	requestCounters := []rc.RequestCounter{
		NewRequestCounter(21),
		NewRequestCounter(9),
		NewRequestCounter(2839),
	}
	netMonitor := &rc.NetworkMonitor{
		RequestCounters: rc.RequestCounters{
			&requestCounters[0],
			&requestCounters[1],
			&requestCounters[2],
		},
	}
	// Mock time with a fake global time that remains constant. This avoids testing the order in which
	// request counters are logged. For simplicity, additional tests may check whether a single request
	// counter calls Debug() the expected number of times as fake time advances forward. This circumvents
	// the task of concurrently communicating time-steps with multiple goroutines, demanding a
	// complicated sleep and time-step synchronization.
	rc.Clock = tw.FakeTime{Time: defaultTime}
	// Record the unique logging statements from each request counter.
	// Block goroutines after reset(), where all logging messages will be identical.
	logged := map[string]bool{}
	stableTime := defaultTime.Format(rc.TimestampFormat)
	// Craft the expected log which will block.
	resetLog := fmt.Sprintf("From %s to %s [0 min] there have been 0 requests to GCR.", stableTime, stableTime)
	// Use a wait group to sure each request counter has finished recording their debug statements.
	wg := sync.WaitGroup{}
	wg.Add(len(requestCounters))
	// Mock the logrus.Debug function.
	rc.Debug = func(args ...interface{}) {
		msg := fmt.Sprint(args[0])
		if resetLog == msg {
			// Block the current goroutine.
			select {}
		}
		logged[msg] = true
		wg.Done()
	}
	// Define the expected logs.
	expected := map[string]bool{
		fmt.Sprintf("From %s to %s [%d min] there have been %d requests to GCR.", stableTime, stableTime, requestCounters[0].Interval/time.Minute, requestCounters[0].Requests): true,
		fmt.Sprintf("From %s to %s [%d min] there have been %d requests to GCR.", stableTime, stableTime, requestCounters[1].Interval/time.Minute, requestCounters[1].Requests): true,
		fmt.Sprintf("From %s to %s [%d min] there have been %d requests to GCR.", stableTime, stableTime, requestCounters[2].Interval/time.Minute, requestCounters[2].Requests): true,
	}
	// Log all request counters.
	netMonitor.Log()
	// Wait for all goroutines to finish their Debug calls.
	wg.Wait()
	// Ensure all request counters were logged and formed correctly.
	require.EqualValues(t, expected, logged, "Request counter(s) did not log correctly.")
}
