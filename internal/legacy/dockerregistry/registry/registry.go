/*
Copyright 2022 The Kubernetes Authors.

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

package registry

import (
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"

	"sigs.k8s.io/promo-tools/v3/types/image"
)

// A RegInvImage is a map containing all of the image names, and their
// associated digest-to-tags mappings. It is the simplest view of a Docker
// Registry, because the keys are just the image.Names (where each image.Name does
// *not* include the registry name, because we already key this by the
// RegistryName in MasterInventory).
//
// The image.Name is actually a path name, because it can be "foo/bar/baz", where
// the image name is the string after the last slash (in this case, "baz").
type RegInvImage map[image.Name]DigestTags

// DigestTags is a map where each digest is associated with a TagSlice. It is
// associated with a TagSlice because an image digest can have more than 1 tag
// pointing to it, even within the same image name's namespace (tags are
// namespaced by the image name).
type DigestTags map[image.Digest]TagSlice

// TagSlice is a slice of Tags.
type TagSlice []image.Tag

// TagSet is a set of Tags.
type TagSet map[image.Tag]interface{}

// ToYAML displays a RegInvImage as YAML, but with the map items sorted
// alphabetically.
func (rii *RegInvImage) ToYAML(o YamlMarshalingOpts) string {
	images := rii.ToSorted()

	var b strings.Builder
	for _, image := range images {
		fmt.Fprintf(&b, "- name: %s\n", image.Name)
		fmt.Fprintf(&b, "  dmap:\n")
		for _, digestEntry := range image.Digests {
			if o.BareDigest {
				fmt.Fprintf(&b, "    %s:", digestEntry.Hash)
			} else {
				fmt.Fprintf(&b, "    %q:", digestEntry.Hash)
			}

			switch len(digestEntry.Tags) {
			case 0:
				fmt.Fprintf(&b, " []\n")
			default:
				if o.SplitTagsOverMultipleLines {
					fmt.Fprintf(&b, "\n")
					for _, tag := range digestEntry.Tags {
						fmt.Fprintf(&b, "    - %s\n", tag)
					}
				} else {
					fmt.Fprintf(&b, " [")
					for i, tag := range digestEntry.Tags {
						if i == len(digestEntry.Tags)-1 {
							fmt.Fprintf(&b, "%q", tag)
						} else {
							fmt.Fprintf(&b, "%q, ", tag)
						}
					}
					fmt.Fprintf(&b, "]\n")
				}
			}
		}
	}

	return b.String()
}

// ToCSV is like ToYAML, but instead of printing things in an indented
// format, it prints one image on each line as a CSV. If there is a tag pointing
// to the image, then it is printed next to the image on the same line.
//
// Example:
// a@sha256:0000000000000000000000000000000000000000000000000000000000000000,a:1.0
// a@sha256:0000000000000000000000000000000000000000000000000000000000000000,a:latest
// b@sha256:1111111111111111111111111111111111111111111111111111111111111111,-
func (rii *RegInvImage) ToCSV() string {
	images := rii.ToSorted()

	var b strings.Builder
	for _, image := range images {
		for _, digestEntry := range image.Digests {
			if len(digestEntry.Tags) > 0 {
				for _, tag := range digestEntry.Tags {
					fmt.Fprintf(&b, "%s@%s,%s:%s\n",
						image.Name,
						digestEntry.Hash,
						image.Name,
						tag)
				}
			} else {
				fmt.Fprintf(&b, "%s@%s,-\n", image.Name, digestEntry.Hash)
			}
		}
	}

	return b.String()
}

// ToSorted converts a RegInvImage type to a sorted structure.
func (rii *RegInvImage) ToSorted() []ImageWithDigestSlice {
	images := make([]ImageWithDigestSlice, 0)

	for name, dmap := range *rii {
		var digests []Digest
		for k, v := range dmap {
			var tags []string
			for _, tag := range v {
				tags = append(tags, string(tag))
			}

			sort.Strings(tags)

			digests = append(digests, Digest{
				Hash: string(k),
				Tags: tags,
			})
		}
		sort.Slice(digests, func(i, j int) bool {
			return digests[i].Hash < digests[j].Hash
		})

		images = append(images, ImageWithDigestSlice{
			Name:    string(name),
			Digests: digests,
		})
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].Name < images[j].Name
	})

	return images
}

// ImageWithDigestSlice uses a slice of digests instead of a map, allowing its
// contents to be sorted.
type ImageWithDigestSlice struct {
	Name    string
	Digests []Digest
}

type Digest struct {
	Hash string
	Tags []string
}

// TODO: Review/optimize/de-dupe (https://github.com/kubernetes-sigs/promo-tools/pull/351)
type ImageWithParentDigestSlice struct {
	Name          string
	parentDigests []parentDigest
}

// TODO: Review/optimize/de-dupe (https://github.com/kubernetes-sigs/promo-tools/pull/351)
type parentDigest struct {
	hash     string
	children []string
}

// FilterParentImages filters the given ImageWithDigestSlice and only returns the images of
// who contain manifest lists (aka: fat-manifests). This is determined by inspecting the
// "mediaType" of every image manifest in the registry.
//
// TODO: Review/optimize/de-dupe (https://github.com/kubernetes-sigs/promo-tools/pull/351)
func FilterParentImages(registry image.Registry, images *[]ImageWithDigestSlice) ([]ImageWithParentDigestSlice, error) {
	// If an image is found to have this specific media type, it is a parent, and will be saved.
	// All other images are not of interest.
	mediaType := `"mediaType": "application/vnd.docker.distribution.manifest.list.v2+json"`
	results := make([]ImageWithParentDigestSlice, 0)

	// Split the registry into prefix and postfix
	// For example: "us.gcr.io/k8s-artifacts-prod/addon-builder"
	// prefix: "us.gcr.io"
	// postfix: "/k8s-artifacts-prod/addon-builder"
	firstSlash := strings.IndexByte(string(registry), '/')
	registryPrefix := registry[:firstSlash]
	registryPostfix := registry[firstSlash:]

	// This is the number of goroutines that will be making curl requests over every image digest.
	numWorkers := runtime.NumCPU() * 2
	wg := new(sync.WaitGroup)
	signal := make(chan ImageWithParentDigestSlice, 500)

	filterImage := func(img ImageWithDigestSlice, c chan ImageWithParentDigestSlice) {
		defer wg.Done()
		imageLocation := fmt.Sprintf("%s/%s", registryPostfix, img.Name)

		// This is a standard endpoint all container registries must adopt. This will reveal
		// information about the manifest for the particular image. The output is in JSON form,
		// with a field "mediaType" that reveals if the given image is a parent image.
		manifestEndpoint := fmt.Sprintf("https://%s/v2%s/manifests/", registryPrefix, imageLocation)
		result := ImageWithParentDigestSlice{img.Name, []parentDigest{}}
		for _, digest := range img.Digests {
			var response string
			numRetries := 8
			imgEndpoint := manifestEndpoint + digest.Hash
			for numRetries > 0 {
				out, err := exec.Command("curl", imgEndpoint).Output()
				if err == nil {
					response = string(out)
					break
				}
				fmt.Println("something went wrong...")
				fmt.Println("curl response:", err)
				fmt.Println("retrying,", imgEndpoint)
				numRetries--
			}

			if response == "" {
				fmt.Println("Could not reach endpoint:", imgEndpoint)
				continue
			}

			if !strings.Contains(response, mediaType) {
				continue
			}

			// A new parent has been found.
			parent := parentDigest{
				hash:     digest.Hash,
				children: []string{},
			}

			// Parse all children by looking for digest fields within response.
			children := strings.Split(response, "},")
			for _, child := range children {
				digestIdx := strings.LastIndex(child, `"digest":`)
				if digestIdx != -1 {
					childHash := child[digestIdx+11 : digestIdx+82]
					parent.children = append(parent.children, childHash)
				}
			}

			result.parentDigests = append(result.parentDigests, parent)
		}

		c <- result
	}

	fmt.Printf("Searching for parent images with %d workers...\n", numWorkers)
	received := 0

	// Engage all workers.
	for i := 0; i < numWorkers; i++ {
		img := (*images)[i]
		go filterImage(img, signal)
		wg.Add(1)
	}

	// When a worker finishes, create another.
	for i := numWorkers; i < len(*images); i++ {
		found := <-signal
		received++
		if len(found.parentDigests) > 0 {
			results = append(results, found)
			fmt.Println("FOUND parent:", found.Name)
		}

		img := (*images)[i]
		go filterImage(img, signal)
		wg.Add(1)
	}

	// Gather all results.
	wg.Wait()
	for found := range signal {
		received++
		if len(found.parentDigests) > 0 {
			results = append(results, found)
			fmt.Println("FOUND parent:", found.Name)
		}

		if received == len(*images) {
			break
		}
	}

	return results, nil
}

// TODO: Review/optimize/de-dupe (https://github.com/kubernetes-sigs/promo-tools/pull/351)
func ValidateParentImages(registry image.Registry, images []ImageWithParentDigestSlice) {
	numWorkers := runtime.NumCPU() * 2
	wg := new(sync.WaitGroup)
	signal := make(chan string, 500)

	validateImg := func(image ImageWithParentDigestSlice, c chan string) {
		defer wg.Done()
		if IsParentImageValid(registry, image) {
			fmt.Println("PASSED - ", image.Name)
			c <- ""
			return
		}

		fmt.Println("FAILED - ", image.Name)
		c <- image.Name
	}

	fmt.Printf("Inspecting children of %d parents...\n", len(images))
	received := 0

	// Engage all workers.
	for i := 0; i < numWorkers; i++ {
		img := images[i]
		go validateImg(img, signal)
		wg.Add(1)
	}

	// When a worker finishes, create another.
	for i := numWorkers; i < len(images); i++ {
		found := <-signal
		received++
		if len(found) > 0 {
			fmt.Println("FAILED ", found)
		}

		img := images[i]
		go validateImg(img, signal)
		wg.Add(1)
	}

	// Gather all results.
	wg.Wait()
	for found := range signal {
		received++
		if len(found) > 0 {
			fmt.Println("FAILED ", found)
		}

		if received == len(images) {
			break
		}
	}
}

// IsParentImageValid only returns true if all child images, from the parent's
// manifest list, are from the same image location.
// Example:
//		VALID 	parent=gcr.io/foo/bar child=gcr.io/foo/bar
// 		INVALID parent=gcr.io/foo/bar child=gcr.io/foo/bar/foo
//
// TODO: Review/optimize/de-dupe (https://github.com/kubernetes-sigs/promo-tools/pull/351)
func IsParentImageValid(registry image.Registry, img ImageWithParentDigestSlice) bool {
	// Split the registry into prefix and postfix
	// For example: "us.gcr.io/k8s-artifacts-prod/addon-builder"
	// prefix: "us.gcr.io"
	// postfix: "/k8s-artifacts-prod/addon-builder"
	firstSlash := strings.IndexByte(string(registry), '/')
	registryPrefix := registry[:firstSlash]
	registryPostfix := registry[firstSlash:]

	imageLocation := fmt.Sprintf("%s/%s", registryPostfix, img.Name)
	manifestEndpoint := fmt.Sprintf("https://%s/v2%s/manifests/", registryPrefix, imageLocation)
	for _, parent := range img.parentDigests {
		for _, childHash := range parent.children {
			var response string

			// This is a standard endpoint all container registries must adopt. This will reveal
			// information about the manifest for the particular image. The output is in JSON form,
			// with a field "mediaType" that reveals if the given image is a parent image.
			imgEndpoint := manifestEndpoint + childHash
			retries := 8

			// Inspect the image. If it doesn't exist, we know the parent child relationship
			// can't be assumed.
			for retries > 0 {
				out, err := exec.Command("curl", imgEndpoint).Output()
				if err == nil {
					response = string(out)
					break
				}

				fmt.Println("trouble obtaining child manifest...")
				fmt.Println("curl response:", err)
				fmt.Println("retrying ", imgEndpoint)
				retries--
			}

			if retries == 0 {
				fmt.Println("Failed to reach ", imgEndpoint)
				fmt.Println("skipping...")
				continue
			}

			if !strings.Contains(response, "mediaType") {
				return false
			}
		}
	}

	return true
}

// YamlMarshalingOpts holds options for tweaking the YAML output.
type YamlMarshalingOpts struct {
	// Render multiple tags on separate lines. I.e.,
	// prefer
	//
	//    sha256:abc...:
	//    - one
	//    - two
	//
	// over
	//
	//    sha256:abc...: ["one", "two"]
	//
	// If there is only 1 tag, it will be on one line in brackets (e.g.,
	// '["one"]').
	SplitTagsOverMultipleLines bool

	// Do not quote the digest. I.e., prefer
	//
	//    sha256:...:
	//
	// over
	//
	//    "sha256:...":
	//
	BareDigest bool
}
