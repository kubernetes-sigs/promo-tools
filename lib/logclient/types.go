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
	"io"
	"log"
)

// LoggingFacility bundles 3 loggers together.
type LoggingFacility struct {
	LogInfo  *log.Logger
	LogError *log.Logger
	LogAlert *log.Logger

	logClient io.Closer
}

// New returns a new LoggingFacility, based on the given loggers.
func New(
	logInfo, logError, logAlert *log.Logger,
	logClient io.Closer,
) *LoggingFacility {
	return &LoggingFacility{
		LogInfo:   logInfo,
		LogError:  logError,
		LogAlert:  logAlert,
		logClient: logClient,
	}
}

// Close implements the "Close" method for the LoggingFacility. This just calls
// Close() on the "logClient".
func (loggingFacility *LoggingFacility) Close() error {
	return loggingFacility.logClient.Close()
}
