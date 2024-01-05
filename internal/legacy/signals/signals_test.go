//go:build !windows
// +build !windows

// Note: this build on unix/linux systems

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

package signals_test

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/promo-tools/v4/internal/legacy/signals"
)

func TestLogSignal(t *testing.T) {
	// Define what a test looks like.
	type sigTest struct {
		signal    os.Signal // Incoming signal to observe.
		expected  []string  // Expected logs to be produced.
		terminate bool      // Determine if signals.LogSignals() should return.
	}
	// Create multiple tests.
	// NOTE: We are unable to observe SIGKILL or SIGSTOP, therefore we will not
	// test with these syscalls.
	sigTests := []sigTest{
		{
			signal: syscall.SIGIO,
			expected: []string{
				fmt.Sprintf("Encoutered signal: %s", syscall.SIGIO.String()),
			},
			terminate: false,
		},
		{
			signal: syscall.SIGALRM,
			expected: []string{
				fmt.Sprintf("Encoutered signal: %s", syscall.SIGALRM.String()),
			},
			terminate: false,
		},
		{
			signal: syscall.SIGALRM,
			expected: []string{
				fmt.Sprintf("Encoutered signal: %s", syscall.SIGALRM.String()),
			},
			terminate: false,
		},
		{
			signal: syscall.SIGQUIT,
			expected: []string{
				fmt.Sprintf("Encoutered signal: %s", syscall.SIGQUIT.String()),
				fmt.Sprintf("Exiting from signal: %s", syscall.SIGQUIT.String()),
			},
			terminate: true,
		},
		{
			signal: syscall.SIGINT,
			expected: []string{
				fmt.Sprintf("Encoutered signal: %s", syscall.SIGINT.String()),
				fmt.Sprintf("Exiting from signal: %s", syscall.SIGINT.String()),
			},
			terminate: true,
		},
		{
			signal: syscall.SIGABRT,
			expected: []string{
				fmt.Sprintf("Encoutered signal: %s", syscall.SIGABRT.String()),
				fmt.Sprintf("Exiting from signal: %s", syscall.SIGABRT.String()),
			},
			terminate: true,
		},
	}
	// Capture all logs.
	logs := []string{}
	// Mock logrus.Debug statements.
	signals.Debug = func(args ...interface{}) {
		logs = append(logs, fmt.Sprint(args...))
	}
	// Determine if LogSignal invoked Stop().
	terminated := func() bool {
		return <-signals.ExitChannel
	}
	// Used for enforcing defaults before each test.
	reset := func() {
		logs = []string{}
	}
	// Run through all tests.
	for _, st := range sigTests {
		reset()
		// Log the test signal.
		signals.LogSignal(st.signal)
		// Ensure the logs are correct.
		require.EqualValues(t, st.expected, logs, "Unexpected signal observation logs.")
		if st.terminate {
			// Ensure Stop() was invoked if the test specifies.
			require.True(t, terminated(), "LogSignal did not terminate on exit signal.")
		}
	}
}

// TestLogSignals ensures LogSignals() can handle multiple incoming signals and terminates either by
// receiving an exit signal or explicit call to Stop().
func TestLogSignals(t *testing.T) {
	// Capture concurrent function termination.
	wg := sync.WaitGroup{}
	terminated := false
	// Ensure the test waits for logSignals to finish executing.
	logSignals := func() {
		signals.LogSignals()
		terminated = true
		wg.Done()
	}
	// Start logging.
	wg.Add(1)
	go logSignals()
	// Pass multiple non-exit signals, ensuring LogSignals is consuming each. Otherwise,
	// the SignalChannel will block and the test will fail.
	signals.SignalChannel <- syscall.SIGBUS
	signals.SignalChannel <- syscall.SIGALRM
	signals.SignalChannel <- syscall.SIGSYS
	signals.SignalChannel <- syscall.SIGIO
	// Force exit.
	signals.Stop()
	wg.Wait()
	// Ensure termination happened.
	require.True(t, terminated, "LogSignals() did not terminate on call to Stop()")

	// Reset termination bool for new test.
	terminated = false
	// Start logging.
	wg.Add(1)
	go logSignals()
	// Pass multiple non-exit signals, ensuring LogSignals is consuming each. Otherwise,
	// the SignalChannel will block and the test will fail.
	signals.SignalChannel <- syscall.SIGTTOU
	signals.SignalChannel <- syscall.SIGCHLD
	signals.SignalChannel <- syscall.SIGPIPE
	// Pass an exit signal.
	signals.SignalChannel <- syscall.SIGINT
	wg.Wait()
	// Ensure termination happened.
	require.True(t, terminated, "LogSignals() did not terminate when given an exit signal")
}
