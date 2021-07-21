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

package reqcounter

import "sync"

// RequestCounter records the number of HTTP requests to GCR.
type RequestCounter struct {
	mutex    sync.Mutex
	requests uint64
}

var (
	// enableCounting will only become true if the Init function is called. This allows
	// requests to be counted and logged.
	enableCounting = false
	// counter will continuously be modified by the Increment function to count all
	// HTTP requests to GCR.
	counter = RequestCounter{}
)

// Init allows request counting to begin.
func Init() {
	enableCounting = true
	counter = RequestCounter{
		mutex:    sync.Mutex{},
		requests: 0,
	}
}

// Increment increases the request counter by 1, signifying an HTTP
// request to GCR has been made.
func Increment() {
	if enableCounting {
		counter.mutex.Lock()
		counter.requests++
		counter.mutex.Unlock()
	}
}
