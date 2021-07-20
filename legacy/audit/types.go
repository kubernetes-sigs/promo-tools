/*
Copyright 2020 The Kubernetes Authors.

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

package audit

import (
	"github.com/sirupsen/logrus"
	reg "sigs.k8s.io/k8s-container-image-promoter/legacy/dockerregistry"
	"sigs.k8s.io/k8s-container-image-promoter/legacy/logclient"
	"sigs.k8s.io/k8s-container-image-promoter/legacy/remotemanifest"
	"sigs.k8s.io/k8s-container-image-promoter/legacy/report"
	"sigs.k8s.io/k8s-container-image-promoter/legacy/stream"
)

// GcrReadingFacility holds functions used to create streams for reading the
// repository and manifest list.
//
// nolint[lll]
type GcrReadingFacility struct {
	ReadRepo         func(*reg.SyncContext, reg.RegistryContext) stream.Producer
	ReadManifestList func(*reg.SyncContext, *reg.GCRManifestListContext) stream.Producer
}

// ServerContext holds all of the initialization data for the server to start
// up.
type ServerContext struct {
	ID                     string
	RemoteManifestFacility remotemanifest.Facility
	ErrorReportingFacility report.ReportingFacility
	LoggingFacility        logclient.LoggingFacility
	GcrReadingFacility     GcrReadingFacility
}

// PubSubMessageInner is the inner struct that holds the actual Pub/Sub
// information.
type PubSubMessageInner struct {
	Data []byte `json:"data,omitempty"`
	ID   string `json:"id"`
}

// PubSubMessage is the payload of a Pub/Sub event.
type PubSubMessage struct {
	Message      PubSubMessageInner `json:"message"`
	Subscription string             `json:"subscription"`
}

// PersistantLoggingInfo holds peristand stats about the behavior of the auditor.
// Since the auditor is a continuously running program, this data will begin
// collection upon startup and lost on shutdown. Such data is only collected when
// invoked with the --verbose flag.
type PersistantLoggingInfo struct {
	VerboseLogging bool
	NumGCRQueries  uint64
}

// Debug only logs the given message if the VerboseLogging option is true.
func (l PersistantLoggingInfo) Debug(args ...interface{}) {
	if l.VerboseLogging {
		logrus.Debug(args...)
	}
}

const (
	// LogName is the auditing log name to use. This is the name that comes up
	// for "gcloud logging logs list".
	LogName = "cip-audit-log"
)

var (
	// PersistantAuditInfo is used to hold persistant logs for the auditor.
	// This is set as a global variable to allow all parts of the auditor
	// access to access logging info.
	PersistantAuditInfo = PersistantLoggingInfo{
		VerboseLogging: false,
		NumGCRQueries:  0,
	}
)
