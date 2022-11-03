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
	"io"
	"math/rand"
	"net/http"
	"runtime"
	"time"
)

const (
	// targetStatusCode is the GCR status code for too many request.
	// Source: https://cloud.google.com/docs/quota#quota_errors
	targetStatusCode int = 429
)

var queries = []string{
	"https://us.gcr.io/v2/k8s-artifacts-prod/addon-builder/tags/list",
	"https://gcr.io/v2/k8s-staging-autoscaling/addon-resizer-amd64/tags/list",
	"https://gcr.io/v2/k8s-staging-cloud-provider-gcp/gcp-filestore-csi-driver/tags/list",
}

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
	start := time.Now()
	spawnWorkers(numWorkers, c)
	requests := numWorkers

	// Continuously make HTTP requests.
	for {
		// Reveal the total number of HTTP requests so far.
		fmt.Println("Requests: ", requests)

		// Wait for response.
		msg := <-c
		if msg.statusCode == targetStatusCode {
			t := time.Now()
			elapsed := t.Sub(start)
			fmt.Println("We were throttled by GCR!")
			fmt.Println("Unique Endpoints: ", len(queries))
			fmt.Println("Took: ", elapsed.Minutes(), "minutes")
			fmt.Println("Status Code: ", targetStatusCode)
			fmt.Println("Body: ", msg.body)
			fmt.Println("Time to ")
			break
		}

		// Spawn a new worker.
		go worker(c)
		requests++
	}
}

// getRandQuery randomly selects a query from the list of queries.
func getRandQuery() string {
	i := rand.Intn(len(queries))
	return queries[i]
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
	query := getRandQuery()
	resp, err := http.Get(query)
	if err != nil {
		fmt.Println("Encountered an error during HTTP GET request: ", err)
	}

	defer resp.Body.Close()

	// Parse the request.
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Encountered an error when reading response body: ", err)
	}

	// Build the response message.
	c <- message{
		body:       string(bodyBytes),
		statusCode: resp.StatusCode,
	}
}
