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
	"log"
	"os"
)

// FakeLogClient is a fake log client. Its sole purpose is to implement a NOP
// "Close()" method, for tests.
type FakeLogClient struct{}

// Close is a NOP (there is nothing to close).
func (fakeLogClient *FakeLogClient) Close() error { return nil }

// NewFakeLoggingFacility returns a new LoggingFacility, but whose resources are
// all local (stdout), with a FakeLogClient.
func NewFakeLoggingFacility() *LoggingFacility {

	logInfo := log.New(os.Stdout, "FAKE-INFO", log.LstdFlags)
	logError := log.New(os.Stdout, "FAKE-ERROR", log.LstdFlags)
	logAlert := log.New(os.Stdout, "FAKE-ALERT", log.LstdFlags)

	return New(logInfo, logError, logAlert, &FakeLogClient{})
}
