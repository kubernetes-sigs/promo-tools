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

package audit

import (
	"net/url"

	"sigs.k8s.io/k8s-container-image-promoter/lib/logclient"
	"sigs.k8s.io/k8s-container-image-promoter/lib/report"
)

// ServerContext holds all of the initialization data for the server to start
// up.
type ServerContext struct {
	ID                     string
	RepoURL                *url.URL
	RepoBranch             string
	ThinManifestDirPath    string
	ErrorReportingFacility report.ReportingFacility
	LoggingFacility        logclient.LoggingFacility
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

const (
	cloneDepth = 1
	// LogName is the auditing log name to use. This is the name that comes up
	// for "gcloud logging logs list".
	LogName = "cip-audit-log"
)
