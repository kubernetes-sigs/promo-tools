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

package signals

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
)

// Watch concurrenlty logs debug statements when encountering interrupt signals from the OS.
// This function relies on the os/signal package which may not capture SIGKILL and SIGSTOP.
func Watch() {
	c := make(chan os.Signal, 1)
	// Observe all signals, excluding SIGKILL and SIGSTOP.
	signal.Notify(c)
	// Continuously watch for signals.
	go func() {
		logrus.Debug("Watching for OS Signals...")
		for sig := range c {
			logrus.Debug("Encoutered signal: ", sig.String())
			// If the observed signal is a termination signal, we are
			// expected to handle this by exiting the program. If we didn't
			// exit, the program would only stop upon receiving a SIGKILL.
			if sig == syscall.SIGHUP ||
				sig == syscall.SIGINT ||
				sig == syscall.SIGABRT ||
				sig == syscall.SIGILL ||
				sig == syscall.SIGQUIT ||
				sig == syscall.SIGTERM ||
				sig == syscall.SIGSEGV ||
				sig == syscall.SIGTSTP {
				logrus.Debug("Exiting from signal: ", sig.String())
				os.Exit(0)
			}
		}
	}()
}
