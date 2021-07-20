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

// PersistentLogging holds stats about the behavior of the auditor.
// These stats will start to be collected upon startup.
type PersistentLogging struct {
	verboseLogging bool        // Determines if the user passed the --verbose flag, allowing persistent logs.
	mutex          sync.Mutex  // Required to grab the queries after each measurement window.
	measurement    Measurement // The measurement structure holding the amount of times GCR was queried.
}

// Measurement holds the logging information about number of queries per measurment window.
type Measurement struct {
	numQueries uint64
	quota      uint64
}

// overQuota returns true when the number of queries surpasses the defined quota.
func (m Measurement) overQuota() bool {
	return m.numQueries > m.quota
}

// Debug only logs the given message if the verboseLogging option is true.
func Debug(args ...interface{}) {
	if PersistentAuditInfo.verboseLogging {
		logrus.Debug(args...)
	}
}

// IncrementGCRQuery adds the given number to the persistent audit logs only
// if verboseLogging is enabled.
func IncrementGCRQuery(n int) {
	// Only capture queries when verboseLogging is enabled
	if !PersistentAuditInfo.verboseLogging {
		return
	}
	// Grab the lock to prevent race conditions.
	PersistentAuditInfo.mutex.Lock()
	// Increment the number of queries.
	PersistentAuditInfo.measurement.numQueries += uint64(n)
	PersistentAuditInfo.mutex.Unlock()
}

// queryLogger records the number of queries within the specified
// measurement window.
func queryLogger() {
	// This logger must not terminate unless the program exists.
	for {
		// Grab the measurement.
		PersistentAuditInfo.mutex.Lock()
		// Log the number of queries.
		msg := fmt.Sprintf("Recorded %d Queries to GCR within %d min.", PersistentAuditInfo.measurement.numQueries, GCRMeasurementWindow/time.Minute)
		Debug(msg)
		// Record when the auditor surpasses the GCR query limit.
		if PersistentAuditInfo.measurement.overQuota() {
			msg = fmt.Sprintf("Quota limit %d req every %d min surpassed. Number of queries recorded: %d", GCRQueryLimit, GCRMeasurementWindow/time.Minute, PersistentAuditInfo.measurement.numQueries)
			Debug(msg)
		}
		// Reset the measurement.
		PersistentAuditInfo.measurement.numQueries = 0
		PersistentAuditInfo.mutex.Unlock()
		// Wait for the query window.
		time.Sleep(GCRMeasurementWindow)
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

// PersistentAuditInfo is used to hold persistent logs for the auditor.
// This is set as a global variable to allow all parts of the auditor
// access to logging info.
var PersistentAuditInfo = &PersistentLogging{
	verboseLogging: false,
	mutex:          sync.Mutex{},
	measurement: Measurement{
		numQueries: 0,
		quota:      GCRQueryLimit,
	},
}
