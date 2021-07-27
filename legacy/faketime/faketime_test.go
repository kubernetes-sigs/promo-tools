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

package faketime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	ft "sigs.k8s.io/k8s-container-image-promoter/legacy/faketime"
)

// sleepingProcess holds the amount of time to sleep, as well as a unique identity to track
// when it wakes up.
type sleepingProcess struct {
	duration time.Duration
	id       int
}

// sleep blocks the sleeping process, and inserts its id into the given map when unblocked.
func (p sleepingProcess) sleep(m map[int]bool) {
	ft.Sleep(p.duration)
	m[p.id] = true
}

type sleepingProcesses []sleepingProcess

// createSleepingProcesses returns when all provided sleeping processes are asleep.
func (sp sleepingProcesses) sleep(m map[int]bool) {
	for _, p := range sp {
		ft.SleepGroup.Add(1)
		go p.sleep(m)
	}
	// Ensure all sleeping processes are actually asleep.
	ft.SleepGroup.Wait()
}

func TestAdvance(t *testing.T) {
	errorMessage := "The expected sleeping processes did not unblock."
	// This test initializes many sleeping processes and advances a single fixed amount of time.
	ft.Init()
	timeStep := time.Second * 30
	// Record which sleeping processes wake up from sleep.
	awake := map[int]bool{}
	// Generate expected record after advancing time.
	expected := map[int]bool{0: true, 1: true}
	// Create multiple sleeping processes.
	sp := sleepingProcesses{
		{
			duration: time.Microsecond,
			id:       0,
		},
		{
			duration: time.Second,
			id:       1,
		},
		{
			duration: time.Minute,
			id:       2,
		},
		{
			duration: time.Hour,
			id:       3,
		},
	}
	// Put all processes to sleep.
	sp.sleep(awake)

	// Advance time by the given time step.
	ft.Advance(timeStep)
	// Ensure only the first two sleeping processes woke up.
	require.EqualValues(t, expected, awake, errorMessage)
	// Stop all processes.
	ft.Close()

	// This test makes several time-steps, while adding new sleeping proccesses each step.
	ft.Init()
	timeSteps := []time.Duration{time.Second, time.Hour, time.Minute}
	step := 0
	// Record which sleeping processes wake up from sleep.
	awake = map[int]bool{}
	// Begin with a few sleeping processes.
	sp = sleepingProcesses{
		{
			duration: time.Second * 2,
			id:       0,
		},
		{
			duration: time.Microsecond,
			id:       1,
		},
		{
			duration: time.Hour,
			id:       2,
		},
	}
	// Put all processes to sleep.
	sp.sleep(awake)

	// Advance time by the first time-step.
	ft.Advance(timeSteps[step])
	step++
	// Determin which sleeping processes should now be awake.
	expected = map[int]bool{1: true}
	// Ensure the correct sleeping proccesses are awake.
	require.EqualValues(t, expected, awake, errorMessage)

	// Add another sleeping process.
	sp = sleepingProcesses{
		{
			duration: time.Hour + time.Second,
			id:       3,
		},
	}
	sp.sleep(awake)

	// Advance time by another time-step.
	ft.Advance(timeSteps[step])
	step++
	// Determin which sleeping processes should now be awake.
	expected = map[int]bool{0: true, 1: true, 2: true}
	// Ensure the correct sleeping proccesses are awake.
	require.EqualValues(t, expected, awake, errorMessage)

	// Add another sleeping process.
	sp = sleepingProcesses{
		{
			duration: time.Millisecond,
			id:       4,
		},
	}
	sp.sleep(awake)

	// Advance time by the final time-step.
	ft.Advance(timeSteps[step])
	// Determin which sleeping processes should now be awake.
	expected = map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true}
	// Ensure the correct sleeping proccesses are awake.
	require.EqualValues(t, expected, awake, errorMessage)

	// Stop all processes.
	ft.Close()
}
