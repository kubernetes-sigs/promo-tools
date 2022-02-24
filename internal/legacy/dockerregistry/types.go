/*
Copyright 2019 The Kubernetes Authors.

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

package inventory

import (
	"sync"

	cr "github.com/google/go-containerregistry/pkg/v1/types"
	grafeaspb "google.golang.org/genproto/googleapis/grafeas/v1"
	"gopkg.in/src-d/go-git.v4/plumbing"

	"sigs.k8s.io/promo-tools/v3/internal/legacy/gcloud"
	"sigs.k8s.io/promo-tools/v3/internal/legacy/stream"
	"sigs.k8s.io/promo-tools/v3/types/image"
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

// ImageSizeError contains ImageSizeCheck information on images that are either
// over the promoter's max image size or have an invalid size of 0 or less.
type ImageSizeError struct {
	MaxImageSize    int
	OversizedImages map[string]int
	InvalidImages   map[string]int
}

// ImageVulnError contains ImageVulnCheck information on images that contain a
// vulnerability with a severity level at or above the defined threshold.
type ImageVulnError struct {
	ImageName      image.Name
	Digest         image.Digest
	OccurrenceName string
	Vulnerability  *grafeaspb.VulnerabilityOccurrence
}

// ImageVulnProducer is used by ImageVulnCheck to get the vulnerabilities for
// an image and allows for custom vulnerability producers for testing.
type ImageVulnProducer func(
	edge PromotionEdge,
) ([]*grafeaspb.Occurrence, error)

// CapturedRequests holds a map of all PromotionRequests that were generated. It
// is used for both -dry-run and testing.
type CapturedRequests map[PromotionRequest]int

// CollectedLogs holds all the Errors that are generated as the promoter runs.
type CollectedLogs struct {
	Errors Errors
}

// SyncContext is the main data structure for performing the promotion.
type SyncContext struct {
	Threads           int
	Confirm           bool
	UseServiceAccount bool
	Inv               MasterInventory
	InvIgnore         []image.Name
	RegistryContexts  []RegistryContext
	SrcRegistry       *RegistryContext
	Tokens            map[RootRepo]gcloud.Token
	DigestMediaType   DigestMediaType
	DigestImageSize   DigestImageSize
	ParentDigest      ParentDigest
	Logs              CollectedLogs
}

// PreCheck represents a check function to run against a pull request that
// modifies the promoter manifests before oking promotion of the changes.
//
// Run runs the defined check and returns an error if the check fails, returns
// nil otherwise.
type PreCheck interface {
	Run() error
}

// ImageVulnCheck implements the PreCheck interface and checks against
// images that have known vulnerabilities.
type ImageVulnCheck struct {
	SyncContext       SyncContext
	PullEdges         map[PromotionEdge]interface{}
	SeverityThreshold int
	FakeVulnProducer  ImageVulnProducer
}

// ImageSizeCheck implements the PreCheck interface and checks against
// images that are larger than a size threshold (controlled by the
// max-image-size flag).
type ImageSizeCheck struct {
	MaxImageSize    int
	DigestImageSize DigestImageSize
	PullEdges       map[PromotionEdge]interface{}
}

// ImageRemovalCheck implements the PreCheck interface and checks against
// pull requests that attempt to remove any images from the promoter manifests.
type ImageRemovalCheck struct {
	GitRepoPath    string
	MasterSHA      plumbing.Hash
	PullRequestSHA plumbing.Hash
	PullEdges      map[PromotionEdge]interface{}
}

// PromotionEdge represents a promotion "link" of an image repository between 2
// registries.
type PromotionEdge struct {
	SrcRegistry RegistryContext
	SrcImageTag ImageTag

	Digest image.Digest

	DstRegistry RegistryContext
	DstImageTag ImageTag
}

// VertexProperty describes the properties of an Edge, with respect to the state
// of the world.
type VertexProperty struct {
	// Pqin means that the entire path, including the registry name, image
	// name, and tag, in that combination, exists.
	PqinExists      bool
	DigestExists    bool
	PqinDigestMatch bool
	BadDigest       image.Digest
	OtherTags       TagSlice
}

// RootRepo is the toplevel Docker repository (e.g., gcr.io/foo (GCR domain name
// + GCP project name).
type RootRepo string

// MasterInventory stores multiple RegInvImage elements, keyed by RegistryName.
type MasterInventory map[image.Registry]RegInvImage

// A RegInvImage is a map containing all of the image names, and their
// associated digest-to-tags mappings. It is the simplest view of a Docker
// Registry, because the keys are just the image.Names (where each image.Name does
// *not* include the registry name, because we already key this by the
// RegistryName in MasterInventory).
//
// The image.Name is actually a path name, because it can be "foo/bar/baz", where
// the image name is the string after the last slash (in this case, "baz").
type RegInvImage map[image.Name]DigestTags

// ImageTag is a combination of the image.Name and Tag.
type ImageTag struct {
	Name image.Name
	Tag  image.Tag
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
	// should be moved to re-point to the digest that we want to promote as
	// defined in the manifest. It can be thought of a Delete followed by an
	// Add.
	Move = iota
	// Delete represents those tags that are not in the manifest and should thus
	// be removed and deleted. This is a kind of "demotion".
	Delete = iota
)

const (
	// ThinManifestDepth specifies the number of items in a path if we split the
	// path into its parts, starting from the "topmost" folder given as an
	// argument to -thin-manifest-dir. E.g., a well-formed path is something
	// like:
	//
	//  ["", "manifests", "foo", "promoter-manifests.yaml"]
	//
	// . This is a result of some path handling/parsing logic in
	// ValidateThinManifestDirectoryStructure().
	ThinManifestDepth = 4
)

// PromotionRequest contains all the information required for any type of
// promotion (or demotion!) (involving any TagOp).
type PromotionRequest struct {
	TagOp          TagOp
	RegistrySrc    image.Registry
	RegistryDest   image.Registry
	ServiceAccount string
	ImageNameSrc   image.Name
	ImageNameDest  image.Name
	Digest         image.Digest
	DigestOld      image.Digest // Only for tag moves.
	Tag            image.Tag
}

// Manifest stores the information in a manifest file (describing the
// desired state of a Docker Registry).
type Manifest struct {
	// Registries contains the source and destination (Src/Dest) registry names.
	// There must be at least 2 registries: 1 source registry and 1 or more
	// destination registries.
	Registries []RegistryContext `yaml:"registries,omitempty"`
	Images     []Image           `yaml:"images,omitempty"`

	// Hidden fields; these are data structure optimizations that are populated
	// from the fields above. As they are redundant, there is no point in
	// storing this information in YAML.
	SrcRegistry *RegistryContext
	Filepath    string
}

// ToRegInvImage converts a Manifest into a RegInvImage.
func (manifest *Manifest) ToRegInvImage() RegInvImage {
	rii := make(RegInvImage)
	for _, img := range manifest.Images {
		rii[img.Name] = img.Dmap
	}
	return rii
}

// ThinManifest is a more secure Manifest because it does not define the
// Images[] directly, but moves it to a separate location. The idea is to define
// a ThinManifest type as a YAML in one folder, and to define the []Image in
// another folder, and to have far stricter ACLs for the ThinManifest type.
// Then, PRs modifying just the []Image YAML won't be able to modify the
// src/destination repos or the credentials tied to them.
type ThinManifest struct {
	Registries []RegistryContext `yaml:"registries,omitempty"`
	// Store actual image data somewhere else.
	//
	// NOTE: "ImagesPath" is deprecated. It does nothing and will be
	// removed in a future release. The images are always stored in a
	// directory structure as follows:
	//
	//       foo
	//       ├── images
	//       │   ├── a
	//       │   │   └── images.yaml
	//       │   ├── b
	//       │   │   └── images.yaml
	//       │   ├── c
	//       │   │   └── images.yaml
	//       │   └── d
	//       │       └── images.yaml
	//       └── manifests
	//           ├── a
	//           │   └── promoter-manifest.yaml
	//           ├── b
	//           │   └── promoter-manifest.yaml
	//           ├── c
	//           │   └── promoter-manifest.yaml
	//           └── d
	//               └── promoter-manifest.yaml
	//
	// where "foo" is the toplevel folder holding all thin manifsets.
	// That is, every manifest must be bifurcated into 2 parts, the
	// "image" and "manifest" part, and these parts must be stored
	// separately.

	ImagesPath string `yaml:"imagesPath,omitempty"`
}

// Image holds information about an image. It's like an "Object" in the OOP
// sense, and holds all the information relating to a particular image that we
// care about.
type Image struct {
	Name image.Name `yaml:"name"`
	Dmap DigestTags `yaml:"dmap,omitempty"`
}

// Images is a slice of Image types.
type Images []Image

// RegistryImagePath is the registry name and image name, without the tag. E.g.
// "gcr.io/foo/bar/baz/image".
type RegistryImagePath string

// DigestTags is a map where each digest is associated with a TagSlice. It is
// associated with a TagSlice because an image digest can have more than 1 tag
// pointing to it, even within the same image name's namespace (tags are
// namespaced by the image name).
type DigestTags map[image.Digest]TagSlice

// RegistryContext holds information about a registry, to be written in a
// manifest file.
type RegistryContext struct {
	Name           image.Registry `yaml:"name,omitempty"`
	ServiceAccount string         `yaml:"service-account,omitempty"`
	Token          gcloud.Token   `yaml:"-"`
	Src            bool           `yaml:"src,omitempty"`
}

// GCRManifestListContext is used only for reading GCRManifestList information
// from GCR, in the function ReadGCRManifestLists.
type GCRManifestListContext struct {
	RegistryContext RegistryContext
	ImageName       image.Name
	Tag             image.Tag
	Digest          image.Digest
}

// DigestMediaType holds media information about a Digest.
type DigestMediaType map[image.Digest]cr.MediaType

// DigestImageSize holds information about the size of an image in bytes.
type DigestImageSize map[image.Digest]int

// ParentDigest holds a map of the digests of children to parent digests. It is
// a reverse mapping of ManifestLists, which point to all the child manifests.
type ParentDigest map[image.Digest]image.Digest

// TagSlice is a slice of Tags.
type TagSlice []image.Tag

// TagSet is a set of Tags.
type TagSet map[image.Tag]interface{}

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
	image.Registry, // srcRegistry
	image.Name, // srcImage
	RegistryContext, // destRegistryContext (need service acc)
	image.Name, // destImage
	image.Digest,
	image.Tag,
	TagOp,
) stream.Producer

// ImageWithDigestSlice uses a slice of digests instead of a map, allowing its
// contents to be sorted.
type ImageWithDigestSlice struct {
	name    string
	digests []digest
}

type digest struct {
	hash string
	tags []string
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

// GCRPubSubPayload is the message payload sent to a Pub/Sub topic by a GCR.
type GCRPubSubPayload struct {
	Action string `json:"action"`

	// The payload field is "digest", but really it is a FQIN like
	// "gcr.io/linusa/small@sha256:35f442d8d56cc7a2d4000f3417d71f44a730b900f3df440c09a9c40c42c40f86".
	FQIN string `json:"digest,omitempty"`

	// Similarly, the field "tag is always a PQIN.
	//
	// Example:
	// "gcr.io/linusa/small:a".
	PQIN string `json:"tag,omitempty"`

	// Everything leading up to either the tag or digest.
	//
	// Example:
	// Given "us.gcr.io/k8s-artifacts-prod/foo/bar:1.0", this would be
	// "us.gcr.io/k8s-artifacts-prod/foo/bar".

	Path string

	// Image digest, if any.
	Digest image.Digest

	// Tag, if any.
	Tag image.Tag
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

// Various conversion functions.
