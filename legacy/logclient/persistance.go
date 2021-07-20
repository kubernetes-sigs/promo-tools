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

package logclient

import (
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// PersistentLoggingInfo holds peristand stats about the behavior of the auditor.
// Since the auditor is a continuously running program, this data will begin
// collection upon startup and lost on shutdown. Such data is only collected when
// invoked with the --verbose flag.
type PersistentLoggingInfo struct {
	verboseLogging bool             // Determins if the user passed the --verbose flag, allowing persistent logs.
	mutex          sync.Mutex       // Required to grab the queries after each measurement window.
	measurement    QueryMeasurement // The measurement structure holding the amount of times GCR was queried.
}

// QueryMeasurement holds the logging information about number of queries per measurment window.
type QueryMeasurement struct {
	startTime  time.Time
	recorded   bool
	numQueries uint64
}

// Debug only logs the given message if the verboseLogging option is true.
func Debug(args ...interface{}) {
	if PersistentAuditInfo.verboseLogging {
		logrus.Debug(args...)
	}
}

// CountGCRQuery adds the given number to the persistent audit logs only
// if verboseLogging is enabled.
func CountGCRQuery(n int) {
	// Only capture queries when verboseLogging is enabled
	if !PersistentAuditInfo.verboseLogging {
		return
	}
	// Grab and the lock to prevent race conditions.
	PersistentAuditInfo.mutex.Lock()
	defer PersistentAuditInfo.mutex.Unlock()
	// Determin if the current measurement has already been recorded.
	if !PersistentAuditInfo.measurement.recorded {
		PersistentAuditInfo.measurement.numQueries += uint64(n)
		return
	}
	// Create a new measurement.
	PersistentAuditInfo.measurement = QueryMeasurement{
		startTime:  time.Now(),
		recorded:   false,
		numQueries: uint64(n)}
}

// queryLogger records the number of queries within the specified
// measurement window.
func queryLogger() {
	// This logger must not terminate unless the program exists.
	for {
		// Wait for the query window.
		time.Sleep(GCRMeasurementWindow)
		// Grab the number of queries, and mark as recorded.
		var numQueries uint64
		PersistentAuditInfo.mutex.Lock()
		numQueries = PersistentAuditInfo.measurement.numQueries
		PersistentAuditInfo.measurement.recorded = true
		PersistentAuditInfo.mutex.Unlock()
		// Log the number of queries.
		msg := fmt.Sprintf("Recorded %d Queries to GCR within %d min.", numQueries, GCRMeasurementWindow/time.Minute)
		Debug(msg)
		// Record when the auditor surpasses the GCR query limit.
		if numQueries > GCRQueryLimit {
			msg = fmt.Sprintf("Quota limit %d req every %d min surpassed. Number of queries recorded: %d", GCRQueryLimit, GCRMeasurementWindow/time.Minute, numQueries)
			Debug(msg)
		}
	}
}

// EnablePersistentLogging configures the persistent logger to capture and
// and display logging extra information.
func EnablePersistentLogging() {
	// Run the logger in the background.
	go queryLogger()
	// Allow debugging statements.
	PersistentAuditInfo.verboseLogging = true
}

const (
	// GCRMeasurementQindow specifies the length of time to batch queries to GCR. Since Google's
	// container registry specifies a query limit of 50,000 HTTP requests per 10 min, the window
	// for recording queries is set to 10 min.
	// Source: https://cloud.google.com/container-registry/quotas
	GCRMeasurementWindow = time.Minute * 10
	// GCRQueryLimit is the number of HTTP requests available to a single IP address within the
	// specified GCRQueryWindow. For GCR, this is 50,000 HTTP requests.
	// Source: https://cloud.google.com/container-registry/quotas
	GCRQueryLimit = 50000
)

var (
	// PersistentAuditInfo is used to hold persistent logs for the auditor.
	// This is set as a global variable to allow all parts of the auditor
	// access to access logging info.
	PersistentAuditInfo = &PersistentLoggingInfo{
		verboseLogging: false,
		mutex:          sync.Mutex{},
		measurement: QueryMeasurement{
			startTime:  time.Now(),
			recorded:   true, // Force a new measurement to be created on the first query.
			numQueries: 0,
		},
	}
)
