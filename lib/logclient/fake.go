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

// FakeLogClient is a fake log client.
type FakeLogClient struct {
	// Because this is fake, there is no actual logging client.
	loggers [3]*log.Logger
}

// Close is a NOP (there is nothing to close).
func (c *FakeLogClient) Close() error { return nil }

// GetInfoLogger exposes the internal Info logger.
func (c *FakeLogClient) GetInfoLogger() *log.Logger {
	return c.loggers[IndexLogInfo]
}

// GetErrorLogger exposes the internal Error logger.
func (c *FakeLogClient) GetErrorLogger() *log.Logger {
	return c.loggers[IndexLogError]
}

// GetAlertLogger exposes the internal Alert logger.
func (c *FakeLogClient) GetAlertLogger() *log.Logger {
	return c.loggers[IndexLogAlert]
}

// NewFakeLogClient returns a new FakeLogClient.
func NewFakeLogClient() *FakeLogClient {
	c := FakeLogClient{}

	c.loggers[IndexLogInfo] = log.
		New(os.Stderr, "FAKE-INFO", log.LstdFlags)
	c.loggers[IndexLogError] = log.
		New(os.Stderr, "FAKE-ERROR", log.LstdFlags)
	c.loggers[IndexLogAlert] = log.
		New(os.Stderr, "FAKE-ALERT", log.LstdFlags)

	return &c
}
