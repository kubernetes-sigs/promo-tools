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

package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"runtime"
)

const (
	// query is a fast query to GCR asking for all tags for the image addon-builder
	// within the k8s-artifacts-prod top-level registry.
	query string = "https://us.gcr.io/v2/k8s-artifacts-prod/addon-builder/tags/list"
	// targetStatusCode is the GCR status code for too many request.
	// Source: https://cloud.google.com/docs/quota#quota_errors
	targetStatusCode int = 429
)

// message holds information about the HTTP response.
type message struct {
	body       string
	statusCode int
}

// main attempts to exceed the quota limits of GCR by continuously
// sending HTTP requests until the quota is reached.
func main() {
	// Determine how many workers should be used.
	numWorkers := runtime.NumCPU() * 2
	c := make(chan message, numWorkers)
	// Create all the workers.
	spawnWorkers(numWorkers, c)
	requests := numWorkers
	// Continuously make HTTP requests.
	for {
		// Reveal the total number of HTTP requests so far.
		fmt.Println("Requests: ", requests)
		// Wait for response.
		msg := <-c
		if msg.statusCode == targetStatusCode {
			fmt.Println("We were throttled by GCR!")
			fmt.Println("Status Code: ", targetStatusCode)
			fmt.Println("Body: ", msg.body)
			break
		}
		// Spawn a new worker.
		go worker(c)
		requests++
	}
}

// spawnWorkers invokes n concurrent workers.
func spawnWorkers(n int, c chan message) {
	for n > 0 {
		go worker(c)
		n--
	}
}

// worker sends an HTTP request to GCR and forwards the
// response to the given channel.
func worker(c chan message) {
	resp, err := http.Get(query)
	if err != nil {
		fmt.Println("Encountered an error during HTTP GET request: ", err)
	}
	defer resp.Body.Close()
	// Parse the request.
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Encountered an error when reading response body: ", err)
	}
	// Build the response message.
	c <- message{
		body:       string(bodyBytes),
		statusCode: resp.StatusCode,
	}
}
