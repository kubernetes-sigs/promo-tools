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

	rc "sigs.k8s.io/promo-tools/v4/internal/legacy/reqcounter"
	tw "sigs.k8s.io/promo-tools/v4/internal/legacy/timewrapper"
)

// defaultThreshold should be used as a default request counter threshold.
const defaultThreshold = 10000

// defaultTime should be used as a timestamp for all request counters.
// The actual time represents: September 22, 2002 at 05:03:16.
var defaultTime, _ = time.Parse("020106 150405", "020106 150405") //nolint: errcheck

// NewRequestCounter returns a new request counter with the given number of requests.
// All other object fields are set to default values.
func NewRequestCounter(requests uint64) rc.RequestCounter {
	return rc.RequestCounter{
		Mutex:     sync.Mutex{},
		Requests:  requests,
		Since:     defaultTime,
		Interval:  time.Second,
		Threshold: defaultThreshold,
	}
}

func TestInit(t *testing.T) {
	rc.Init()
	// Ensure request counting is enabled.
	require.True(t, rc.EnableCounting, "Init did not enable counting.")
	// Ensure request counters are created.
	require.NotEmpty(t, rc.NetMonitor.RequestCounters, "Init did not create any request counters within the global Monitor.")
	// Ensure at least one request counter uses the QuotaWindowShort.
	foundQuotaWindowShort, foundQuotaWindowLong := false, false
	for _, requestCounter := range rc.NetMonitor.RequestCounters {
		switch requestCounter.Interval {
		case rc.QuotaWindowShort:
			foundQuotaWindowShort = true
		case rc.QuotaWindowLong:
			foundQuotaWindowLong = true
		}
	}
	require.True(t, foundQuotaWindowShort, "No request counters are using the Interval: QuotaWindowShort.")
	require.True(t, foundQuotaWindowLong, "No request counters are using the Interval: QuotaWindowLong.")
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
	require.Equal(t, netMonitor, rc.NetMonitor, "Request counters were modified while counting was disabled.")
	// Ensure the Increment function actually increments each request counter's requests field.
	rc.EnableCounting = true
	rc.Increment()
	require.Equal(t, expectedNetMonitor, rc.NetMonitor, "Request counters were not incremented correctly.")
}

func TestFlush(t *testing.T) {
	// Create a local invocation of time.
	requestCounter := NewRequestCounter(33)
	// Mock the logrus.Debug function.
	debugCalls := 0
	rc.Debug = func(_ ...interface{}) {
		debugCalls++
	}
	requestCounter.Flush()
	// Ensure logrus.Debug was called.
	require.Equal(t, 1, debugCalls, "Flush() failed to trigger a debug statement.")
	// Ensure the request counter is reset, where time advances and the requests are zeroed.
	require.Equal(t, uint64(0), requestCounter.Requests, "Calling Flush() did not reset the request counter to 0.")
	require.True(t, defaultTime.Before(requestCounter.Since), "Calling Flush() did not reset the request counter timestamp.")

	// Create a persistent request counter.
	requestCounter = NewRequestCounter(66)
	requestCounter.Threshold = 0
	requestCounter.Flush()
	// Ensure the request counter did not reset.
	require.Equal(t, uint64(66), requestCounter.Requests, "Calling Flush() reset the requests of a non-resettable request counter.")
	require.True(t, defaultTime.Equal(requestCounter.Since), "Calling Flush() reset the timestamp of a non-resettable request counter.")

	// Create a request counter that exceeded a threshold.
	requestCounter = NewRequestCounter(600)
	requestCounter.Threshold = 599
	// Reset debug counter.
	debugCalls = 0
	// Mock logrus.Warn.
	warnCalls := 0
	rc.Warn = func(_ ...interface{}) {
		warnCalls++
	}
	requestCounter.Flush()
	// Ensure both logrus.Debug and logrus.Warn was called.
	require.Equal(t, 1, debugCalls, "Flush() failed to trigger a debug statement after exceeding the threshold.")
	require.Equal(t, 1, warnCalls, "Flush() failed to trigger a warning statement after exceeding the threshold.")
}

func TestRequestCounterIncrement(t *testing.T) {
	// Create a simple request counter.
	requestCounter := NewRequestCounter(36)
	// Create a request counter expected after Increment is called.
	expected := NewRequestCounter(37)
	// Increment the counter.
	requestCounter.Increment()
	// Ensure the request counter was incremented.
	require.Equal(t, &expected, &requestCounter, "The request counter failed to increment its request field.")
}

func TestCycle(t *testing.T) {
	// Define the variables of each test.
	type cycleTest struct {
		interval  time.Duration // Specify logging interval.
		requests  []int         // The number of HTTP request to simulate for each cycle.
		threshold uint64        // When to fire a warning log.
	}
	// Define all tests.
	cycleTests := []cycleTest{
		{
			interval:  rc.QuotaWindowShort,
			requests:  []int{3, 7, 50, 1},
			threshold: defaultThreshold,
		},
		{
			interval:  rc.QuotaWindowLong,
			requests:  []int{2, 622, 5, 8},
			threshold: defaultThreshold,
		},
		{
			interval:  time.Second,
			requests:  []int{9, 0, 13, 700},
			threshold: defaultThreshold,
		},
		{
			interval:  time.Minute * 30,
			requests:  []int{9, 0, 13, 700},
			threshold: defaultThreshold,
		},
		{
			interval:  time.Hour * 10,
			requests:  []int{9, 0, 13, 700},
			threshold: defaultThreshold,
		},
		{
			interval:  time.Hour * 10,
			requests:  []int{9, 0, 13, 700},
			threshold: 0,
		},
	}
	// Simulate HTTP requests by repeatedly incrementing the request counter.
	mockNetworkTraffic := func(requestCounter *rc.RequestCounter, requests int) {
		for requests > 0 {
			requestCounter.Increment()
			requests--
		}
	}

	// Run all tests.
	for _, ct := range cycleTests {
		// Create a simple request counter.
		requestCounter := NewRequestCounter(0)
		requestCounter.Interval = ct.interval
		requestCounter.Threshold = ct.threshold
		// Collect logging statements.
		logs := []string{}
		// Mock logrus.Debug calls.
		rc.Debug = func(args ...interface{}) {
			logs = append(logs, fmt.Sprint(args[0]))
		}
		// Mock time.
		fakeTime := tw.FakeTime{
			Time: defaultTime,
		}
		rc.Clock = &fakeTime
		// Collect expected logs.
		expected := []string{}
		// Repeatedly run sleep/log cycles.
		testClock := defaultTime
		totalRequests := 0
		for _, requests := range ct.requests {
			// Generate the expected log for this cycle.
			nextClock := testClock.Add(ct.interval)
			expectedRequests := requests
			expectedStartingClock := testClock
			if ct.threshold == 0 {
				// Record a running tally.
				totalRequests += requests
				expectedRequests = totalRequests
				// The starting timestamp must not change.
				expectedStartingClock = defaultTime
			}
			expect := fmt.Sprintf("From %s to %s [%d min] there have been %d requests to GCR.", expectedStartingClock.Format(rc.TimestampFormat), nextClock.Format(rc.TimestampFormat), ct.interval/time.Minute, expectedRequests)
			expected = append(expected, expect)
			testClock = nextClock
			// Simulate HTTP requests.
			mockNetworkTraffic(&requestCounter, requests)
			// Initiate a sleep/log cycle.
			requestCounter.Cycle()
		}
		// Ensure the correct logs were produced.
		require.Equal(t, expected, logs, "The request counter produced malformed logs.")
	}
}
