/*
Copyright 2026 The Kubernetes Authors.

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

package promotion

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry/registry"
	"sigs.k8s.io/promo-tools/v4/internal/legacy/dockerregistry/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// Edge represents a promotion "link" of an image repository between 2
// registries.
type Edge struct {
	SrcRegistry registry.Context
	SrcImageTag ImageTag

	Digest image.Digest

	DstRegistry registry.Context
	DstImageTag ImageTag
}

// ImageTag is a combination of image.Name and Tag.
type ImageTag struct {
	Name image.Name
	Tag  image.Tag
}

// SrcReference returns the fully-qualified source reference for this edge.
func (edge *Edge) SrcReference() string {
	if edge.SrcRegistry.Name == "" || edge.SrcImageTag.Name == "" || edge.Digest == "" {
		return ""
	}

	return fmt.Sprintf(
		"%s/%s@%s",
		edge.SrcRegistry.Name,
		edge.SrcImageTag.Name,
		edge.Digest,
	)
}

// DstReference returns the fully-qualified destination reference for this edge.
func (edge *Edge) DstReference() string {
	if edge.DstRegistry.Name == "" || edge.DstImageTag.Name == "" || edge.Digest == "" {
		return ""
	}

	return fmt.Sprintf(
		"%s/%s@%s",
		edge.DstRegistry.Name,
		edge.DstImageTag.Name,
		edge.Digest,
	)
}

// ToFQIN converts a RegistryName, ImageName, and Digest to form a
// fully-qualified image name (FQIN).
func ToFQIN(registryName image.Registry, imageName image.Name, digest image.Digest) string {
	return string(registryName) + "/" + string(imageName) + "@" + string(digest)
}

// ToPQIN converts a RegistryName, ImageName, and Tag to form a
// partially-qualified image name (PQIN).
func ToPQIN(registryName image.Registry, imageName image.Name, tag image.Tag) string {
	return string(registryName) + "/" + string(imageName) + ":" + string(tag)
}

// ToEdges converts a list of manifests to a set of edges we want to
// try promoting.
func ToEdges(mfests []schema.Manifest) (map[Edge]interface{}, error) {
	edges := make(map[Edge]interface{})
	for _, mfest := range mfests {
		for _, img := range mfest.Images {
			for digest, tagArray := range img.Dmap {
				for _, destRC := range mfest.Registries {
					if destRC == *mfest.SrcRegistry {
						continue
					}

					if len(tagArray) > 0 {
						for _, tag := range tagArray {
							edge := mkEdge(
								*mfest.SrcRegistry,
								destRC,
								img.Name,
								digest,
								tag)
							edges[edge] = nil
						}
					} else {
						edge := mkEdge(
							*mfest.SrcRegistry,
							destRC,
							img.Name,
							digest,
							"",
						)

						edges[edge] = nil
					}
				}
			}
		}
	}

	return CheckOverlappingEdges(edges)
}

func mkEdge(
	srcRC, dstRC registry.Context,
	srcImageName image.Name,
	digest image.Digest,
	tag image.Tag,
) Edge {
	return Edge{
		SrcRegistry: srcRC,
		SrcImageTag: ImageTag{
			Name: srcImageName,
			Tag:  tag,
		},
		Digest:      digest,
		DstRegistry: dstRC,
		DstImageTag: ImageTag{
			Name: srcImageName,
			Tag:  tag,
		},
	}
}

// CheckOverlappingEdges checks for conflicting promotion edges (different
// digests targeting the same destination tag).
func CheckOverlappingEdges(
	edges map[Edge]interface{},
) (map[Edge]interface{}, error) {
	promotionIntent := make(map[string]map[image.Digest][]Edge)
	checked := make(map[Edge]interface{})
	for edge := range edges {
		if edge.DstImageTag.Tag == "" {
			checked[edge] = nil
			continue
		}

		dstPQIN := ToPQIN(edge.DstRegistry.Name,
			edge.DstImageTag.Name,
			edge.DstImageTag.Tag,
		)

		digestToEdges, ok := promotionIntent[dstPQIN]
		if ok {
			digestToEdges[edge.Digest] = append(digestToEdges[edge.Digest], edge)
			promotionIntent[dstPQIN] = digestToEdges
		} else {
			edgeList := []Edge{edge}
			digestToEdges := make(map[image.Digest][]Edge)
			digestToEdges[edge.Digest] = edgeList
			promotionIntent[dstPQIN] = digestToEdges
		}
	}

	overlapError := false
	emptyEdgeListError := false
	for pqin, digestToEdges := range promotionIntent {
		if len(digestToEdges) < 2 {
			for _, edgeList := range digestToEdges {
				switch len(edgeList) {
				case 0:
					logrus.Errorf("no edges for %v", pqin)
					emptyEdgeListError = true
				case 1:
					checked[edgeList[0]] = nil
				default:
					logrus.Infof("redundant promotion: multiple edges want to promote the same digest to the same destination endpoint %v:", pqin)
					for i := range edgeList {
						logrus.Infof("%v", edgeList[i])
					}
					logrus.Infof("using the first one: %v", edgeList[0])
					checked[edgeList[0]] = nil
				}
			}
		} else {
			logrus.Errorf("multiple edges want to promote *different* images (digests) to the same destination endpoint %v:", pqin)
			for digest, edgeList := range digestToEdges {
				logrus.Errorf("  for digest %v:\n", digest)
				for i := range edgeList {
					logrus.Errorf("%v\n", edgeList[i])
				}
			}
			overlapError = true
		}
	}

	if overlapError {
		return nil, errors.New("overlapping edges detected")
	}

	if emptyEdgeListError {
		return nil, errors.New("empty edgeList(s) detected")
	}

	return checked, nil
}

// EdgesToRegInvImage takes the destination endpoints of all edges and converts
// their information to a RegInvImage type. It uses only those edges that are
// trying to promote to the given destination registry.
func EdgesToRegInvImage(
	edges map[Edge]interface{},
	destRegistry string,
) registry.RegInvImage {
	rii := make(registry.RegInvImage)

	destRegistry = strings.TrimRight(destRegistry, "/")

	for edge := range edges {
		var (
			imgName string
			prefix  string
		)

		if strings.HasPrefix(string(edge.DstRegistry.Name), destRegistry) {
			prefix = strings.TrimPrefix(
				string(edge.DstRegistry.Name),
				destRegistry)

			if prefix != "" {
				imgName = prefix + "/" + string(edge.DstImageTag.Name)
			} else {
				imgName = string(edge.DstImageTag.Name)
			}

			imgName = strings.TrimLeft(imgName, "/")
		} else {
			continue
		}

		if rii[image.Name(imgName)] == nil {
			rii[image.Name(imgName)] = make(registry.DigestTags)
		}

		digestTags := rii[image.Name(imgName)]
		if len(edge.DstImageTag.Tag) > 0 {
			digestTags[edge.Digest] = append(
				digestTags[edge.Digest],
				edge.DstImageTag.Tag)
		} else {
			digestTags[edge.Digest] = registry.TagSlice{}
		}
	}

	return rii
}

// GetRegistriesToRead collects all unique Docker repositories we want to read
// from. This way, we don't have to read the entire Docker registry, but only
// those paths that we are thinking of modifying.
func GetRegistriesToRead(edges map[Edge]interface{}) []registry.Context {
	rcs := make(map[registry.Context]interface{})

	for edge := range edges {
		srcReg := edge.SrcRegistry
		srcReg.Name = srcReg.Name +
			"/" +
			image.Registry(edge.SrcImageTag.Name)
		rcs[srcReg] = nil

		dstReg := edge.DstRegistry
		dstReg.Name = dstReg.Name +
			"/" +
			image.Registry(edge.DstImageTag.Name)
		rcs[dstReg] = nil
	}

	rcsFinal := make([]registry.Context, 0, len(rcs))
	for rc := range rcs {
		rcsFinal = append(rcsFinal, rc)
	}

	return rcsFinal
}

// FilterByTag returns a subset of the inventory containing only images that
// have the given tag.
func FilterByTag(rii registry.RegInvImage, tag string) registry.RegInvImage {
	filtered := make(registry.RegInvImage)
	for imgName, digestTags := range rii {
		for digest, tagSlice := range digestTags {
			for _, t := range tagSlice {
				if string(t) == tag {
					if filtered[imgName] == nil {
						filtered[imgName] = make(registry.DigestTags)
					}
					filtered[imgName][digest] = tagSlice
					break
				}
			}
		}
	}
	return filtered
}

// VertexProperty describes the properties of an Edge vertex with respect to
// the state of the registry inventory.
type VertexProperty struct {
	// PqinExists means the tag exists in the registry.
	PqinExists bool
	// DigestExists means the digest exists in the registry.
	DigestExists bool
	// PqinDigestMatch means the tag points to the expected digest.
	PqinDigestMatch bool
	// BadDigest is the digest that the tag currently points to (when mismatched).
	BadDigest image.Digest
	// OtherTags lists tags associated with the digest.
	OtherTags registry.TagSlice
}

// VertexProps determines the properties of each vertex (src and dst) in the
// edge, depending on the state of the world in the inventory.
func (edge *Edge) VertexProps(
	inv map[image.Registry]registry.RegInvImage,
) (srcProps, dstProps VertexProperty) {
	return edge.vertexPropsFor(&edge.SrcRegistry, &edge.SrcImageTag, inv),
		edge.vertexPropsFor(&edge.DstRegistry, &edge.DstImageTag, inv)
}

// vertexPropsFor examines one of the two vertices (src or dst) of an Edge.
func (edge *Edge) vertexPropsFor(
	rc *registry.Context,
	imageTag *ImageTag,
	inv map[image.Registry]registry.RegInvImage,
) VertexProperty {
	p := VertexProperty{}

	rii, ok := inv[rc.Name]
	if !ok {
		return p
	}
	digestTags, ok := rii[imageTag.Name]
	if !ok {
		return p
	}

	if tagSlice, ok := digestTags[edge.Digest]; ok {
		p.DigestExists = true
		p.OtherTags = tagSlice
	}

	for digest, tagSlice := range digestTags {
		for _, tag := range tagSlice {
			if tag == imageTag.Tag {
				p.PqinExists = true
				if digest == edge.Digest {
					p.PqinDigestMatch = true
					p.OtherTags = registry.TagSlice{}
				} else {
					p.BadDigest = digest
				}
			}
		}
	}

	return p
}

// GetPromotionCandidates filters edges to only those that need promotion,
// removing already-promoted edges and detecting errors like tag moves.
func GetPromotionCandidates(
	edges map[Edge]interface{},
	inv map[image.Registry]registry.RegInvImage,
) (map[Edge]interface{}, bool) {
	clean := true

	toPromote := make(map[Edge]interface{})
	for edge := range edges {
		sp, dp := edge.VertexProps(inv)

		// If dst vertex already matches, NOP.
		if dp.PqinDigestMatch {
			logrus.Debugf("edge %v: skipping because it was already promoted (case 1)", edge)
			continue
		}

		// If this edge is for a tagless promotion, skip if the digest exists
		// in the destination.
		if edge.DstImageTag.Tag == "" && dp.DigestExists {
			if !sp.DigestExists {
				logrus.Errorf("edge %v: skipping %s/%s@%s because it was already promoted, but it is still _LOST_ (can't find it in src registry! please backfill it!)",
					edge, edge.SrcRegistry.Name, edge.SrcImageTag.Name, edge.Digest)
			}
			continue
		}

		// If src vertex missing, LOST && NOP.
		if !sp.DigestExists {
			logrus.Errorf("edge %v: skipping %s/%s@%s because it is _LOST_ (can't find it in src registry!)",
				edge, edge.SrcRegistry.Name, edge.SrcImageTag.Name, edge.Digest)
			continue
		}

		if dp.PqinExists {
			if dp.DigestExists {
				if dp.PqinDigestMatch {
					// NOP (already promoted).
					logrus.Debugf("edge %v: skipping because it was already promoted (case 2)", edge)
					continue
				}
				// Tag exists pointing to a different digest, and the target
				// digest also exists separately — this is an error.
				logrus.Errorf("edge %v: tag %s: tag move detected", edge, edge.DstImageTag.Tag)
				clean = false
				continue
			}
			// Tag exists pointing to wrong digest, target digest doesn't
			// exist — tag move attempt, which is not supported.
			logrus.Errorf("edge %v: tag '%s' in dest points to %s, not %s; tag moves are not supported",
				edge, edge.DstImageTag.Tag, dp.BadDigest, edge.Digest)
			clean = false
			continue
		}

		if dp.DigestExists {
			logrus.Infof("edge %v: digest %q already exists, but does not have the tag we want (%s)",
				edge, edge.Digest, dp.OtherTags)
		} else {
			logrus.Infof("edge %v: regular promotion (neither digest nor tag exists in dst)", edge)
		}

		toPromote[edge] = nil
	}

	return toPromote, clean
}
