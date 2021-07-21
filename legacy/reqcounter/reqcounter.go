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
)

// RequestCounter records the number of HTTP requests to GCR.
type RequestCounter struct {
	mutex    sync.Mutex // Lock to prevent race-conditions with concurrent processes.
	requests uint64     // Number of HTTP requests since recording started.
	quota    uint64     // Maximum requests allowed before GCR throttling occurs.
	since    time.Time  // When the current counter began recording requests.
}

// log records the number of HTTP requests found and resets the counter.
func (rc *RequestCounter) log() {
	// Grab the lock to prevent race-conditions.
	rc.mutex.Lock()
	defer rc.mutex.Unlock()
	// Log the number of requests found.
	msg := fmt.Sprintf("Since %s there have been %d requests to GCR.", counter.since.Format("01-02-2006 15:04:05"), counter.requests)
	logrus.Debug(msg)
	// Warn of GCR scaling when requests exceed quota.
	rc.warn()
	// Reset the counter.
	rc.reset()
}

// warn only logs a warning if the number of requests has exceeded the quota.
func (rc *RequestCounter) warn() {
	if rc.requests > rc.quota {
		msg := fmt.Sprintf("The GCR quota of \"%d requests per %d min\" has been exceeded.", quotaLimit, measurementWindow/10)
		logrus.Warn(msg)
	}
}

// reset clears the number of requests and stamps the object with the current time of reset.
// WARNING: To avoid race conditions, only use this method when holding the counter's mutex.
func (rc *RequestCounter) reset() {
	rc.requests = 0
	rc.since = time.Now()
}

const (
	// measurementWindow specifies the length of time to batch requests to GCR. Since Google's
	// Container Registry specifies a quota of 50,000 HTTP requests per 10 min, the window
	// for recording requests is set to 10 min.
	// Source: https://cloud.google.com/container-registry/quotas
	measurementWindow = time.Minute * 10
	// quotaLimit is the number of HTTP requests available to a single IP address within the
	// specified measurement window. For GCR, this is 50,000 HTTP requests.
	// Source: https://cloud.google.com/container-registry/quotas
	quotaLimit = 50000
)

var (
	// enableCounting will only become true if the Init function is called. This allows
	// requests to be counted and logged.
	enableCounting = false
	// counter will continuously be modified by the Increment function to count all
	// HTTP requests to GCR.
	counter = &RequestCounter{}
)

// Init allows request counting to begin.
func Init() {
	enableCounting = true
	counter = &RequestCounter{
		mutex:    sync.Mutex{},
		requests: 0,
		quota:    quotaLimit,
		since:    time.Now(),
	}
	// Trigger the logger to run in the background.
	go requestLogger(measurementWindow)
}

// Increment increases the request counter by 1, signifying an HTTP
// request to GCR has been made.
func Increment() {
	if enableCounting {
		counter.mutex.Lock()
		counter.requests++
		counter.mutex.Unlock()
	}
}

// requestLogger continuously logs the number of recorded HTTP requests every interval.
func requestLogger(interval time.Duration) {
	for {
		time.Sleep(interval)
		counter.log()
	}
}
