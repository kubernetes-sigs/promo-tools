/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package logclient

import (
	"context"

	"cloud.google.com/go/logging"
	"k8s.io/klog"
)

// NewGcpLoggingFacility returns a new LoggingFacility that logs to GCP
// resources. As such, it requires the GCP projectID as well as the logName to
// log to.
func NewGcpLoggingFacility(projectID, logName string) *LoggingFacility {
	gcpLogClient := initGcpLogClient(projectID)

	logInfo := gcpLogClient.Logger(logName).StandardLogger(logging.Info)
	logError := gcpLogClient.Logger(logName).StandardLogger(logging.Error)
	logAlert := gcpLogClient.Logger(logName).StandardLogger(logging.Alert)

	return New(logInfo, logError, logAlert, gcpLogClient)
}

// initGcpLogClient creates a logging client that performs better logging than
// the default behavior on GCP Stackdriver. For instance, logs sent with this
// client are not split up over newlines, and also the severity levels are
// actually understood by Stackdriver.
func initGcpLogClient(projectID string) *logging.Client {

	ctx := context.Background()

	// Creates a client.
	client, err := logging.NewClient(ctx, projectID)
	if err != nil {
		klog.Fatalf("Failed to create client: %v", err)
	}

	return client
}
