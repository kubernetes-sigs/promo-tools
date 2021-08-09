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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
)

var subProjects []string = []string{
	"k8s-staging-addon-manager",
	"k8s-staging-apisnoop",
	"k8s-staging-artifact-promoter",
	"k8s-staging-autoscaling",
	"k8s-staging-boskos",
	"k8s-staging-build-image",
	"k8s-staging-capi-docker",
	"k8s-staging-capi-openstack",
	"k8s-staging-capi-vsphere",
	"k8s-staging-ci-images",
	"k8s-staging-cloud-provider-gcp",
	"k8s-staging-cluster-addons",
	"k8s-staging-cluster-api",
	"k8s-staging-cluster-api-aws",
	"k8s-staging-cluster-api-azure",
	"k8s-staging-cluster-api-do",
	"k8s-staging-cluster-api-gcp",
	"k8s-staging-cluster-api-kubeadm",
	"k8s-staging-cluster-api-nested",
	"k8s-staging-coredns",
	"k8s-staging-cpa",
	"k8s-staging-cri-tools",
	"k8s-staging-csi-secrets-store",
	"k8s-staging-descheduler",
	"k8s-staging-dns",
	"k8s-staging-e2e-test-images",
	"k8s-staging-etcd",
	"k8s-staging-etcdadm",
	"k8s-staging-examples",
	"k8s-staging-experimental",
	"k8s-staging-external-dns",
	"k8s-staging-gateway-api",
	"k8s-staging-git-sync",
	"k8s-staging-infra-tools",
	"k8s-staging-ingress-controller-conformance",
	"k8s-staging-ingress-nginx",
	"k8s-staging-k8s-gsm-tools",
	"k8s-staging-kas-network-proxy",
	"k8s-staging-kind",
	"k8s-staging-kops",
	"k8s-staging-kubeadm",
	"k8s-staging-kubernetes",
	"k8s-staging-kube-state-metrics",
	"k8s-staging-kubetest2",
	"k8s-staging-kustomize",
	"k8s-staging-metrics-server",
	"k8s-staging-mirror",
	"k8s-staging-multitenancy",
	"k8s-staging-networking",
	"k8s-staging-nfd",
	"k8s-staging-npd",
	"k8s-staging-prometheus-adapter",
	"k8s-staging-provider-aws",
	"k8s-staging-provider-azure",
	"k8s-staging-publishing-bot",
	"k8s-staging-releng",
	"k8s-staging-scheduler-plugins",
	"k8s-staging-scl-image-builder",
	"k8s-staging-sig-docs",
	"k8s-staging-sig-storage",
	"k8s-staging-slack-infra",
	"k8s-staging-sp-operator",
	"k8s-staging-storage-migrator",
	"k8s-staging-test-infra",
	"k8s-staging-txtdirect",
}

type request struct {
	registry string
	repo     string
}

type Manifest struct {
	ImageSizeBytes string   `json:"imageSizeBytes"`
	LayerId        string   `json:"layerId"`
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

func (r *request) getQuery() string {
	return fmt.Sprintf("https://%s/v2/%s/tags/list", r.registry, r.repo)
}

func getPayload(resp *http.Response) response {
	body, err := ioutil.ReadAll(resp.Body)
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

func (r *request) countImages() int {
	query := r.getQuery()

	retries := 5
	var resp *http.Response
	var err error

	resp, err = http.Get(query)
	for err != nil {
		if retries == 0 {
			panic(query + " could not be reached.")
		}
		resp, err = http.Get(query)
		retries--
	}

	payload := getPayload(resp)
	totalChildren := len(payload.Child)
	for _, child := range payload.Child {
		c := request{
			r.registry,
			r.repo + "/" + child,
		}
		totalChildren += c.countImages()
	}
	return totalChildren
}

func main() {
	if len(os.Args) == 2 {
		subProjects = []string{os.Args[1]}
	}
	maxRepo := ""
	maxChildren := 0
	for _, subProject := range subProjects {
		r := request{
			registry: "gcr.io",
			repo:     subProject,
		}
		c := r.countImages()
		fmt.Printf("Registry %s has %d image-prefixes.\n", subProject, c)
		if maxChildren < c {
			maxChildren = c
			maxRepo = subProject
		}
	}
	fmt.Println("Max Repo: ", maxRepo)
}
