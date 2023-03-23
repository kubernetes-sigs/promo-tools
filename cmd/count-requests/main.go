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
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

type request struct {
	registry string
	repo     string
}

type Manifest struct {
	ImageSizeBytes string   `json:"imageSizeBytes"`
	LayerID        string   `json:"layerId"`
	MediaType      string   `json:"mediaType"`
	Tag            StrArray `json:"tag"`
	TimeCreatedMs  string   `json:"timeCreatedMs"`
	TimeUploadedMs string   `json:"timeUploadedMs"`
}

type ManifestMap map[string]Manifest

type StrArray []string

type response struct {
	Child    StrArray    `json:"child"`
	Manifest ManifestMap `json:"manifest"`
	Tags     StrArray    `json:"tags"`
	Name     string      `json:"name"`
}

// getSubProjects all sub-projects found in kubernetes/k8s.io.
func getSubProjects() []string {
	var cmd *exec.Cmd
	var out []byte
	var err error

	fmt.Println("Retrieving all kubernetes/k8s.io sub-projects...")

	// Automate error handling.
	handle := func(c *exec.Cmd, e error) {
		if e != nil {
			fmt.Println("Failed to execute: ", c.String())
			os.Exit(1)
		}
	}
	// Create a temporary directory.
	cmd = exec.Command("mktemp", "-d")
	out, err = cmd.Output()
	handle(cmd, err)
	tmpDir := strings.TrimSpace(string(out))
	// Clone the kubernetes/k8s.io repo.
	cmd = exec.Command("git", "clone", "https://github.com/kubernetes/k8s.io.git", tmpDir)
	_, err = cmd.Output()
	handle(cmd, err)
	// List the number of sub-projects in the repo.
	subProjects := fmt.Sprintf("%s/registry.k8s.io/manifests", tmpDir)
	cmd = exec.Command("ls", subProjects)
	out, err = cmd.Output()
	handle(cmd, err)
	// Clear the temporary directory.
	cmd = exec.Command("rm", "-r", tmpDir)
	_, err = cmd.Output()
	handle(cmd, err)
	// Parse the sub-projects into a list of strings.
	results := []string{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		subProject := scanner.Text()
		if subProject != "README.md" {
			results = append(results, subProject)
		}
	}
	return results
}

// genQuery converts the request into an HTTPS query.
func (r *request) genQuery() string {
	return fmt.Sprintf("https://%s/v2/%s/tags/list", r.registry, r.repo)
}

// getPayload converts the HTTP response into a response payload.
func getPayload(resp *http.Response) response {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	var data response
	err = json.Unmarshal(body, &data)
	if err != nil {
		panic(err)
	}
	resp.Body.Close()
	return data
}

func makeRequest(query string) response {
	var resp *http.Response
	var payload response
	var err error

	// Keep requesting until valid response or too many retries.
	for retries := 5; retries >= 1; retries-- {
		resp, err = http.Get(query)
		if err == nil {
			payload = getPayload(resp)
		} else if retries == 0 {
			panic(query + " could not be reached.")
		}
	}

	// Extract the payload from the HTTP response.
	return payload
}

// countQueries returns the total number of HTTP requests needed to validate the request.
// This total comprises of all children image-prefixes + all manifest lists.
func (r *request) countQueries() int {
	query := r.genQuery()
	payload := makeRequest(query)
	// We must first count this current query.
	queries := 1
	// Recurse over children.
	for _, child := range payload.Child {
		c := request{
			r.registry,
			r.repo + "/" + child,
		}
		// Add up queries made by children.
		queries += c.countQueries()
	}
	// IMPORTANT: The Auditor also makes an HTTP request for every manifest.lists it finds.
	// Count all manifest lists we see.
	for _, manifest := range payload.Manifest {
		if strings.Contains(manifest.MediaType, "manifest.list") {
			// Found a manifest list.
			queries++
		}
	}
	return queries
}

func printUsage() {
	fmt.Printf("\nUsage: %s [sub-project]\n\n", os.Args[0])
	fmt.Println(`About: This program finds the total number of HTTP requests to validate a given sub-project.
If no [sub-project] is given, it aggrigates all sub-projects found in kubernetes/k8s.io and
finds the sub-project that requires the most requests to validate.`)
}

func main() {
	var subProject string
	if len(os.Args) > 2 {
		fmt.Println("Invalid number of arguments!")
		printUsage()
		os.Exit(1)
	}
	if len(os.Args) == 2 {
		subProject = os.Args[1]
		r := request{
			registry: "gcr.io",
			repo:     subProject,
		}
		fmt.Printf("The Auditor would make %d queries to GCR.\n", r.countQueries())
		return
	}
	// Find the sub-project requiring the largest number of queries to verify.
	maxQueries := 0
	for _, sp := range getSubProjects() {
		r := request{
			registry: "gcr.io",
			repo:     sp,
		}
		numQueries := r.countQueries()
		fmt.Printf("Sub-project %q requires %d queries.\n", sp, numQueries)
		if maxQueries < numQueries {
			maxQueries = numQueries
			subProject = sp
		}
	}
	fmt.Printf("[MAX] %q takes %d queries to verify.\n", subProject, maxQueries)
}
