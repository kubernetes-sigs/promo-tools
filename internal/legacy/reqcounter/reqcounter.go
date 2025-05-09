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

package reqcounter

import (
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	tw "sigs.k8s.io/promo-tools/v4/internal/legacy/timewrapper"
)

// RequestCounter records the number of HTTP requests to GCR.
type RequestCounter struct {
	// Lock to prevent race-conditions with concurrent processes.
	mutex sync.Mutex

	// Number of HTTP requests since recording started.
	requests uint64

	// When the current request counter began recording requests.
	since time.Time

	// The duration of time between each log.
	interval time.Duration

	// When to warn of a high request count during a logging cycle. Setting a
	// non-zero threshold allows the request counter to reset each interval. If left uninitialized,
	// the request counter will be persistent and never warn or reset.
	threshold uint64
}

// increment adds 1 to the request counter, signifying another call to GCR.
func (rc *RequestCounter) Increment() {
	rc.mutex.Lock()
	rc.requests++
	rc.mutex.Unlock()
}

// Flush records the number of HTTP requests found and resets the request counter.
func (rc *RequestCounter) flush() {
	// Hold onto the lock when reading & writing the request counter.
	rc.mutex.Lock()
	defer rc.mutex.Unlock()

	rc.log()

	// Only allow request counters wi
	if rc.threshold > 0 {
		// Reset the request counter.
		rc.resetWithLockHeld()
	}
}

// log the number of HTTP requests found. If the number of requests exceeds the
// threshold, log an additional warning message.
func (rc *RequestCounter) log() {
	msg := fmt.Sprintf("From %s to %s [%d min] there have been %d requests to GCR.", rc.since.Format(TimestampFormat), Clock.Now().Format(TimestampFormat), rc.interval/time.Minute, rc.requests)
	Debug(msg)
	if rc.threshold > 0 && rc.requests > rc.threshold {
		msg = fmt.Sprintf("The threshold of %d requests has been surpassed.", rc.threshold)
		Warn(msg)
	}
}

// reset clears the request counter and stamps the current time of reset.
// this function should be called with the mutex held.
func (rc *RequestCounter) resetWithLockHeld() {
	rc.requests = 0
	rc.since = Clock.Now()
}

// watch indefinitely performs repeated sleep/log cycles.
func (rc *RequestCounter) watch() {
	// TODO: @tylerferrara create a way to cleanly terminate this goroutine.
	go func() {
		for {
			rc.cycle()
		}
	}()
}

// cycle sleeps for the request counter's interval and flushes itself.
func (rc *RequestCounter) cycle() {
	Clock.Sleep(rc.interval)
	rc.flush()
}

// RequestCounters holds multiple request counters.
type RequestCounters []*RequestCounter

// NetworkMonitor is the primary means of monitoring network traffic between CIP and GCR.
type NetworkMonitor struct {
	RequestCounters RequestCounters
}

// increment adds 1 to each request counter, signifying a new request has been made to GCR.
func (nm *NetworkMonitor) increment() {
	for _, rc := range nm.RequestCounters {
		rc.Increment()
	}
}

// Log begins logging each request counter at their specified intervals.
func (nm *NetworkMonitor) log() {
	for _, rc := range nm.RequestCounters {
		rc.watch()
	}
}

const (
	// QuotaWindowShort specifies the length of time to wait before logging in order to estimate the first
	// GCR Quota of 50,000 HTTP requests per 10 min.
	// NOTE: These metrics are only a rough approximation of the actual GCR quotas. The specific 10min measurement
	// is ambiguous, as the start and end time are not specified in the docs. Therefore, it's impossible for our
	// requests counters to perfectly line up with the actual GCR quota.
	// Source: https://cloud.google.com/container-registry/quotas
	QuotaWindowShort time.Duration = time.Minute * 10
	// QuotaWindowLong specifies the length of time to wait before logging in order to estimate the second
	// GCR Quota of 1,000,000 HTTP requests per day.
	QuotaWindowLong time.Duration = time.Hour * 24
	// TimestampFormat specifies the syntax for logging time stamps of request counters.
	TimestampFormat string = "2006-01-02 15:04:05"
)

var (
	// EnableCounting will only become true if the Init function is called. This allows
	// requests to be counted and logged.
	EnableCounting bool
	// NetMonitor holds all request counters for recording HTTP requests to GCR.
	NetMonitor *NetworkMonitor
	// Debug is defined to simplify testing of logrus.Debug calls.
	Debug func(args ...interface{}) = logrus.Debug
	// Warn is defined to simplify testing of logrus.Warn calls.
	Warn func(args ...interface{}) = logrus.Warn
	// Clock is defined to allow mocking of time functions.
	Clock tw.Time = tw.RealTime{}
)

// Init allows request counting to begin.
func Init() {
	EnableCounting = true

	// Create a request counter for logging traffic every 10mins. This aims to mimic the actual
	// GCR quota, but acts as a rough estimation of this quota, indicating when throttling may occur.
	requestCounters := RequestCounters{
		{
			requests:  0,
			since:     Clock.Now(),
			interval:  QuotaWindowShort,
			threshold: 50000,
		},
		{
			requests:  0,
			since:     Clock.Now(),
			interval:  QuotaWindowLong,
			threshold: 1000000,
		},
		{
			requests: 0,
			since:    Clock.Now(),
			interval: QuotaWindowShort,
		},
	}

	// Create a new network monitor.
	NetMonitor = &NetworkMonitor{
		RequestCounters: requestCounters,
	}

	// Begin logging network traffic.
	NetMonitor.log()
}

// Increment increases the all request counters by 1, signifying an HTTP
// request to GCR has been made.
func Increment() {
	if EnableCounting {
		NetMonitor.increment()
	}
}
