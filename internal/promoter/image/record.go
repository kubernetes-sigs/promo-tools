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

package imagepromoter

import (
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/release-utils/command"
	"sigs.k8s.io/release-utils/version"

	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// Prow job environment variables recorded in promotion records.
const (
	prowJobNameEnv   = "JOB_NAME"
	prowJobTypeEnv   = "JOB_TYPE"
	prowBuildIDEnv   = "BUILD_ID"
	prowProwJobIDEnv = "PROW_JOB_ID"
)

// manifestKey identifies the manifest entry an edge was created from.
type manifestKey struct {
	src    image.Registry
	name   image.Name
	digest image.Digest
}

// recordContext holds the parts of a promotion record that are the same
// for all edges of a run, or only depend on the manifest of an edge.
type recordContext struct {
	promoter *provenance.Promoter
	job      *provenance.Job

	// manifests maps edges to the path of their manifest file.
	manifests map[manifestKey]string

	// descriptors caches the manifest descriptors by path. They are only
	// built for manifests of promoted edges, because a run parses all
	// manifests and each descriptor needs git calls.
	descriptors map[string]*provenance.ResourceDescriptor
}

// newRecordContext collects the promoter, job and manifest information for
// the promotion records of a run.
func newRecordContext(mfests []schema.Manifest) *recordContext {
	info := version.GetVersionInfo()

	rc := &recordContext{
		promoter: &provenance.Promoter{
			Version:   info.GitVersion,
			GitCommit: info.GitCommit,
		},
		job:         prowJob(),
		manifests:   map[manifestKey]string{},
		descriptors: map[string]*provenance.ResourceDescriptor{},
	}

	for i := range mfests {
		mfest := &mfests[i]
		if mfest.SrcRegistry == nil {
			continue
		}

		path := mfest.ImagesFilepath
		if path == "" {
			path = mfest.Filepath
		}

		if path == "" {
			continue
		}

		for _, img := range mfest.Images {
			for digest := range img.Dmap {
				key := manifestKey{src: mfest.SrcRegistry.Name, name: img.Name, digest: digest}
				if _, exists := rc.manifests[key]; !exists {
					rc.manifests[key] = path
				}
			}
		}
	}

	return rc
}

// record returns the promotion record for a group of edges sharing the
// same target identity and digest.
func (rc *recordContext) record(group []promotion.Edge, identity string) *provenance.PromotionRecord {
	edge := group[0]

	tagSet := map[string]struct{}{}

	for i := range group {
		if tag := string(group[i].DstImageTag.Tag); tag != "" {
			tagSet[tag] = struct{}{}
		}
	}

	tags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		tags = append(tags, tag)
	}

	sort.Strings(tags)

	digest := digestMap(edge.Digest)

	return &provenance.PromotionRecord{
		SrcRef: edge.SrcReference(),
		DstRef: identity,
		Digest: string(edge.Digest),
		Tags:   tags,
		Source: &provenance.ResourceDescriptor{
			Name:   string(edge.SrcRegistry.Name) + "/" + string(edge.SrcImageTag.Name),
			Digest: digest,
		},
		Destination: &provenance.ResourceDescriptor{
			Name:   identity,
			Digest: digest,
		},
		Manifest: rc.manifest(manifestKey{
			src:    edge.SrcRegistry.Name,
			name:   edge.SrcImageTag.Name,
			digest: edge.Digest,
		}),
		Promoter: rc.promoter,
		Job:      rc.job,
	}
}

// manifest returns the descriptor of the manifest an edge was read from,
// or nil if it is not known. Descriptors are built on first use.
func (rc *recordContext) manifest(key manifestKey) *provenance.ResourceDescriptor {
	path, ok := rc.manifests[key]
	if !ok {
		return nil
	}

	descriptor, ok := rc.descriptors[path]
	if !ok {
		descriptor = manifestDescriptor(path)
		rc.descriptors[path] = descriptor
	}

	return descriptor
}

// digestMap converts a digest like "sha256:abc" into an in-toto digest set.
func digestMap(digest image.Digest) map[string]string {
	algorithm, value, ok := strings.Cut(string(digest), ":")
	if !ok {
		return nil
	}

	return map[string]string{algorithm: value}
}

// prowJob returns the Prow job running the promotion, or nil outside of Prow.
func prowJob() *provenance.Job {
	name := os.Getenv(prowJobNameEnv)
	if name == "" {
		return nil
	}

	return &provenance.Job{
		Name:      name,
		Type:      os.Getenv(prowJobTypeEnv),
		BuildId:   os.Getenv(prowBuildIDEnv),
		ProwJobId: os.Getenv(prowProwJobIDEnv),
	}
}

// manifestDescriptor describes a manifest file by its path relative to the
// repository root and the commit it was read from. Outside of a git
// repository only the path is recorded.
func manifestDescriptor(path string) *provenance.ResourceDescriptor {
	descriptor := &provenance.ResourceDescriptor{Name: path}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return descriptor
	}

	// git reports the resolved repository root, resolve the path as well.
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}

	dir := filepath.Dir(absPath)

	git := func(args ...string) string {
		res, err := command.NewWithWorkDir(dir, "git", args...).RunSilentSuccessOutput()
		if err != nil {
			logrus.Debugf("Unable to run git %v for manifest %s: %v", args, path, err)

			return ""
		}

		return res.OutputTrimNL()
	}

	root := git("rev-parse", "--show-toplevel")
	if root == "" {
		return descriptor
	}

	if rel, err := filepath.Rel(root, absPath); err == nil {
		descriptor.Name = rel
	}

	if commit := git("rev-parse", "HEAD"); commit != "" {
		descriptor.Digest = map[string]string{"gitCommit": commit}
	}

	descriptor.Uri = repositoryURI(git("remote", "get-url", "origin"))

	return descriptor
}

// repositoryURI returns a git+https URI for an https remote without
// credentials, and nothing for other remotes.
func repositoryURI(remote string) string {
	u, err := url.Parse(remote)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}

	u.User = nil

	return "git+" + u.String()
}
