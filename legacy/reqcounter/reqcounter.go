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
	tw "sigs.k8s.io/k8s-container-image-promoter/legacy/timewrapper"
)

// RequestCounter records the number of HTTP requests to GCR.
// TODO: @tylerferrara in the future, add a field 'persistent bool' to determine if
// the request counter should ever reset.
type RequestCounter struct {
	Mutex    sync.Mutex    // Lock to prevent race-conditions with concurrent processes.
	Requests uint64        // Number of HTTP requests since recording started.
	Since    time.Time     // When the current request counter began recording requests.
	Interval time.Duration // The duration of time between each log.
}

// increment adds 1 to the request counter, signifying another call to GCR.
func (rc *RequestCounter) Increment() {
	rc.Mutex.Lock()
	rc.Requests++
	rc.Mutex.Unlock()
}

// Flush records the number of HTTP requests found and resets the request counter.
func (rc *RequestCounter) Flush() {
	// Hold onto the lock when reading & writing the request counter.
	rc.Mutex.Lock()
	defer rc.Mutex.Unlock()

	// Log the number of requests within this measurement window.
	msg := fmt.Sprintf("From %s to %s [%d min] there have been %d requests to GCR.", rc.Since.Format(TimestampFormat), Clock.Now().Format(TimestampFormat), rc.Interval/time.Minute, rc.Requests)
	Debug(msg)

	// Reset the request counter.
	rc.reset()
}

// reset clears the request counter and stamps the current time of reset.
// TODO: @tylerferrara in the future, use the request counter field 'persistent' to check
// if it must be reset.
func (rc *RequestCounter) reset() {
	rc.Requests = 0
	rc.Since = Clock.Now()
}

// watch continuously logs the request counter at the specified intervals.
func (rc *RequestCounter) watch() {
	go func() {
		for {
			Clock.Sleep(rc.Interval)
			rc.Flush()
		}
	}()
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
func (nm *NetworkMonitor) Log() {
	for _, rc := range nm.RequestCounters {
		rc.watch()
	}
}

const (
	// MeasurementWindow specifies the length of time to wait before logging the request counters. Since Google's
	// Container Registry specifies a quota of 50,000 HTTP requests per 10 min, the window
	// for recording requests is set to 10 min.
	// NOTE: This metric is only a rough approximation of the actual GCR quota. The specific 10min measurement
	// is ambiguous, as the start and end time are not specified in the docs. Therefore, it's impossible for our
	// requests counters to perfectly line up with the actual GCR quota.
	// Source: https://cloud.google.com/container-registry/quotas
	MeasurementWindow time.Duration = time.Minute * 10
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
	// Clock is defined to allow mocking of time functions.
	Clock tw.Time = tw.RealTime{}
)

// Init allows request counting to begin.
func Init() {
	EnableCounting = true

	// Create a request counter for logging traffic every 10mins. This aims to mimic the actual
	// GCR quota, but acts as a rough estimation of this quota, indicating when throttling may occur.
	requestCounter := &RequestCounter{
		Mutex:    sync.Mutex{},
		Requests: 0,
		Since:    Clock.Now(),
		Interval: MeasurementWindow,
	}

	// Create a new network monitor.
	NetMonitor = &NetworkMonitor{
		RequestCounters: RequestCounters{requestCounter},
	}

	// Begin logging network traffic.
	NetMonitor.Log()
}

// Increment increases the all request counters by 1, signifying an HTTP
// request to GCR has been made.
func Increment() {
	if EnableCounting {
		NetMonitor.increment()
	}
}
