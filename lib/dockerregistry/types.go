/*
Copyright 2019 The Kubernetes Authors.

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

package inventory

import (
	"sync"

	"sigs.k8s.io/k8s-container-image-promoter/lib/stream"
)

// RequestResult contains information about the result of running a request
// (e.g., a "gcloud" command, or perhaps in the future, a REST call).
type RequestResult struct {
	Context stream.ExternalRequest
	Errors  Errors
}

// Errors is a slice of Errors.
type Errors []Error

// Error contains slightly more verbosity than a standard "error".
type Error struct {
	Context string
	Error   error
}

// CapturedRequests holds a map of all PromotionRequests that were generated. It
// is used for both -dry-run and testing.
type CapturedRequests map[PromotionRequest]int

// SyncContext is the main data structure for performing the promotion.
type SyncContext struct {
	ManifestPath        string
	Verbosity           int
	Threads             int
	DeleteExtraTags     bool
	DryRun              bool
	UseServiceAccount   bool
	Inv                 MasterInventory
	RegistryContexts    []RegistryContext
	SrcRegistry         *RegistryContext
	Tokens              map[RootRepo]Token
	RenamesDenormalized RenamesDenormalized
}

// RootRepo is the toplevel Docker repository (e.g., gcr.io/foo (GCR domain name
// + GCP project name).
type RootRepo string

// Token is the oauth2 access token used for API calls over HTTP.
type Token string

// MasterInventory stores multiple RegInvImage elements, keyed by RegistryName.
type MasterInventory map[RegistryName]RegInvImage

// A RegInvImage is a map containing all of the image names, and their
// associated digest-to-tags mappings. It is the simplest view of a Docker
// Registry, because the keys are just the ImageNames (where each ImageName does
// *not* include the registry name, because we already key this by the
// RegistryName in MasterInventory).
//
// The ImageName is actually a path name, because it can be "foo/bar/baz", where
// the image name is the string after the last slash (in this case, "baz").
type RegInvImage map[ImageName]DigestTags

// Registry is another way to look at a Docker Registry; it is used during
// Promotion.
type Registry struct {
	RegistryName      string
	RegistryNameLong  RegistryName
	RegInvImageDigest RegInvImageDigest
}

// RegInvFlat is a flattened view of a Docker Registry, where the keys contain
// all 3 attributes --- the image name, digest, and tag.
type RegInvFlat map[ImageDigestTag]interface{}

// ImageDigestTag is a flattened key used by RegInvFlat.
type ImageDigestTag struct {
	ImageName ImageName
	Digest    Digest
	Tag       Tag
}

// RegInvImageTag is keyed by a ImageTag.
type RegInvImageTag map[ImageTag]Digest

// ImageTag is a combination of the ImageName and Tag.
type ImageTag struct {
	ImageName ImageName
	Tag       Tag
}

// RegInvImageDigest is a view of a Docker Reqistry, keyed by ImageDigest.
type RegInvImageDigest map[ImageDigest]TagSlice

// ImageDigest is a combination of the ImageName and Digest.
type ImageDigest struct {
	ImageName ImageName
	Digest    Digest
}

// TagOp is an enum that describes the various types of tag-modifying
// operations. These actions are a bit more low-level, and currently support 3
// operations: adding, moving, and deleting.
type TagOp int

const (
	// Add represents those tags that are freely promotable, without fear of an
	// overwrite (we are only adding tags).
	Add TagOp = iota
	// Move represents those tags that conflict with existing digests, and so
	// must be move to re-point to the digest that we want to promote as defined
	// in the manifest. It can be thought of a Delete followed by an Add.
	Move = iota
	// Delete represents those tags that are not in the manifest and should thus
	// be removed and deleted. This is a kind of "demotion".
	Delete = iota
)

// PromotionRequest contains all the information required for any type of
// promotion (or demotion!) (involving any TagOp).
type PromotionRequest struct {
	TagOp          TagOp
	RegistrySrc    RegistryName
	RegistryDest   RegistryName
	ServiceAccount string
	ImageNameSrc   ImageName
	ImageNameDest  ImageName
	Digest         Digest
	DigestOld      Digest // Only for tag moves.
	Tag            Tag
}

// Manifest stores the information in a manifest file (describing the
// desired state of a Docker Registry).
type Manifest struct {
	// Registries contains the source and destination (Src/Dest) registry names.
	// It is possible that in the future, we support promoting to multiple
	// registries, in which case we would have more than just Src/Dest.
	Registries []RegistryContext `yaml:"registries,omitempty"`
	Images     []Image           `yaml:"images,omitempty"`
	// A rename list can contain a list of paths, where each path is a string.
	//
	// - A rename entry must have at least 2 paths, one for the source, another
	// for at least 1 dest registry.
	//
	// - Any unknown registry entries in here will be considered a parsing
	// error.
	//
	// - Any redundant entries in here will be considered a parsing error. E.g.,
	// "gcr.io/louhi-qa/glbc:gcr.io/louhi-gke-k8s/glbc" is redunant as it is
	// implied already.
	//
	// - The names must be valid paths (no errant punctuation, etc.).
	//
	// - No self-loops allowed (a registry must not appear more than 1 time).
	//
	// - Each name must be the registry+pathname, *without* a trailing slash.
	//
	// Just before the promotion, each rename entry is processed, to update the
	// master inventory entries for the *renamed* images.
	//
	// When fetching data from a renamed image's repository, they are
	// "normalized" to the path as seen in the source registry for that image
	// --- this is so that the set difference logic can be used as-is. Only when
	// the promotion itself is performed, do we "denormalize" at the very last
	// moment by modifying the argument to each destination path.
	Renames []Rename `yaml:"renames,omitempty"`
}

// RenamesDenormalized is a lookup-optimized data structure of rename
// information.
type RenamesDenormalized map[RegistryImagePath]map[RegistryName]ImageName

// Image holds information about an image. It's like an "Object" in the OOP
// sense, and holds all the information relating to a particular image that we
// care about.
type Image struct {
	ImageName ImageName  `yaml:"name"`
	Dmap      DigestTags `yaml:"dmap,omitempty"`
}

// Rename is list of paths, where each path is full
// image name (registry + image name, without the tag).
type Rename []RegistryImagePath

// RegistryImagePath is the registry name and image name, without the tag. E.g.
// "gcr.io/foo/bar/baz/image".
type RegistryImagePath string

// DigestTags is a map where each digest is associated with a TagSlice. It is
// associated with a TagSlice because an image digest can have more than 1 tag
// pointing to it, even within the same image name's namespace (tags are
// namespaced by the image name).
type DigestTags map[Digest]TagSlice

// RegistryContext holds information about a registry, to be written in a
// manifest file.
type RegistryContext struct {
	Name           RegistryName `yaml:"name,omitempty"`
	ServiceAccount string       `yaml:"service-account,omitempty"`
	Token          Token        `yaml:"-"`
	Src            bool         `yaml:"src,omitempty"`
}

// RegistryName is the leading part of an image name that includes the domain;
// it is everything that is not the actual image name itself. E.g.,
// "gcr.io/google-containers".
type RegistryName string

// The ImageName can be just the bare name itself (e.g., "addon-builder" in
// "gcr.io/k8s-image-staging/addon-builder") or the prefix + name
// ("foo/bar/baz/quux" in "gcr.io/hello/foo/bar/baz/quux").
type ImageName string

// Digest is a string that contains the SHA256 hash of a Docker container image.
type Digest string

// Tag is a Docker tag.
type Tag string

// TagSlice is a slice of Tags.
type TagSlice []Tag

// TagSet is a set of Tags.
type TagSet map[Tag]interface{}

// DigestTagsContext holds information about the request that was used to fetch
// the list of digests and associated tags for a particular image. It is used in
// ReadDigestsAndTags().
type DigestTagsContext struct {
	ImageName    ImageName
	RegistryName RegistryName
}

// PopulateRequests is a function that can generate requests used to fetch
// information about a Docker Registry, or to promote images. It basically
// generates the set of "gcloud ..." commands used to manipulate Docker
// Registries.
type PopulateRequests func(
	*SyncContext,
	chan<- stream.ExternalRequest,
	*sync.WaitGroup)

// ProcessRequest is the counterpart to PopulateRequests. It is a function that
// can take a request (generated by PopulateRequests) and process it. In the
// ictual implementation (e.g. in ReadDigestsAndTags()) it closes over some
// other local variables to record the change of state in the Docker Registry
// that was touched by processing the request.
type ProcessRequest func(
	*SyncContext,
	chan stream.ExternalRequest,
	chan<- RequestResult,
	*sync.WaitGroup,
	*sync.Mutex)

// PromotionContext holds all info required to create a stream that would
// produce a stream.Producer, as it relates to an intent to promote an image.
type PromotionContext func(
	RegistryName, // srcRegisttry
	ImageName, // srcImage
	RegistryContext, // destRegistryContext (need service acc)
	ImageName, // destImage
	Digest,
	Tag,
	TagOp) stream.Producer

// Various conversion functions.

// ToRegInvImageDigest converts a Manifest to a RegInvImageDigest.
func (m Manifest) ToRegInvImageDigest() RegInvImageDigest {
	riid := make(RegInvImageDigest)
	for _, image := range m.Images {
		for digest, tagArray := range image.Dmap {
			id := ImageDigest{}
			id.ImageName = image.ImageName
			id.Digest = digest
			riid[id] = tagArray
		}
	}
	return riid
}

// ToRegInvImageTag converts a Manifest to a RegInvImageTag.
func (m Manifest) ToRegInvImageTag() RegInvImageTag {
	riit := make(RegInvImageTag)
	for _, image := range m.Images {
		for digest, tagArray := range image.Dmap {
			for _, tag := range tagArray {
				it := ImageTag{}
				it.ImageName = image.ImageName
				it.Tag = tag
				riit[it] = digest
			}
		}
	}
	return riit
}

// ToRegInvImageDigest takes a RegInvImage and converts it to a
// RegInvImageDigest.
func (ri RegInvImage) ToRegInvImageDigest() RegInvImageDigest {
	riid := make(RegInvImageDigest)
	for imageName, digestTags := range ri {
		for digest, tagArray := range digestTags {
			id := ImageDigest{}
			id.ImageName = imageName
			id.Digest = digest
			riid[id] = tagArray
		}
	}
	return riid
}

// ToRegInvImageTag converts a RegInvImage to a RegInvImageTag.
func (ri RegInvImage) ToRegInvImageTag() RegInvImageTag {
	riit := make(RegInvImageTag)
	for imageName, digestTags := range ri {
		for digest, tagArray := range digestTags {
			for _, tag := range tagArray {
				it := ImageTag{}
				it.ImageName = imageName
				it.Tag = tag
				riit[it] = digest
			}
		}
	}
	return riit
}

// ToRegInvImageTag converts a RegInvImageDigest to a RegInvImageTag.
func (riid RegInvImageDigest) ToRegInvImageTag() RegInvImageTag {
	riit := make(RegInvImageTag)
	for imageDigest, tagArray := range riid {
		for _, tag := range tagArray {
			it := ImageTag{}
			it.ImageName = imageDigest.ImageName
			it.Tag = tag
			riit[it] = imageDigest.Digest
		}
	}
	return riit
}

// ToRegInvImageDigest converts a RegInvImageTag to a RegInvImageDigest.
func (riit RegInvImageTag) ToRegInvImageDigest() RegInvImageDigest {
	riid := make(RegInvImageDigest)
	for imageTag, digest := range riit {
		id := ImageDigest{}
		id.ImageName = imageTag.ImageName
		id.Digest = digest

		if tagSlice, ok := riid[id]; ok {
			riid[id] = append(tagSlice, imageTag.Tag)
		} else {
			riid[id] = TagSlice{imageTag.Tag}
		}
	}
	return riid
}
