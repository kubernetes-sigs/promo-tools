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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/carabiner-dev/collector/envelope/bundle"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/release-utils/version"

	options "sigs.k8s.io/promo-tools/v4/promoter/image/options"
	"sigs.k8s.io/promo-tools/v4/promoter/image/promotion"
	"sigs.k8s.io/promo-tools/v4/promoter/image/provenance"
	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	"sigs.k8s.io/promo-tools/v4/promoter/image/schema"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// maxSummarySize limits how much of an existing verification summary is
// read.
const maxSummarySize = 1 << 20

// WriteVerificationSummaries writes a signed SLSA verification summary for
// each promoted digest, index and platform manifests alike, to the
// canonical registry, when enabled. It is written once: a digest that has
// a verification summary, for example because it was promoted before under
// another tag, keeps it. The summaries are made of the discoveries and
// provenance results of the source images, by source reference.
func (di *DefaultPromoterImplementation) WriteVerificationSummaries(
	ctx context.Context,
	opts *options.Options,
	mfests []schema.Manifest,
	edges map[promotion.Edge]any,
	discoveries map[string]*provenance.Discovery,
	outcomes map[string]*provenance.ImageProvenance,
) error {
	if !opts.VerificationSummaries {
		return nil
	}

	if !opts.SignImages {
		logrus.Info("Not writing verification summaries (--sign=false)")

		return nil
	}

	if len(edges) == 0 {
		return nil
	}

	//nolint:contextcheck
	if err := di.ensureAttestationSigner(opts); err != nil {
		return fmt.Errorf("initializing attestation signer: %w", err)
	}

	policies := newPolicyContext(mfests)
	kpromoVersion := version.GetVersionInfo().GitVersion
	now := time.Now()

	groups := groupEdgesByIdentityDigest(edges, withTagless)

	// The children of an index that are promoted digests of their own get
	// their own summaries.
	promoted := make(map[string]bool, len(groups))
	for _, group := range groups {
		promoted[targetIdentity(&group[0])+"@"+string(group[0].Digest)] = true
	}

	// Promoted images are no promotion candidates in later runs, so one
	// failure must not keep the others from being written.
	var (
		g        errgroup.Group
		mu       sync.Mutex
		errs     []error
		children = map[string]*childSummary{}
	)

	g.SetLimit(opts.MaxSignatureOps)

	addErr := func(err error) {
		mu.Lock()
		defer mu.Unlock()

		errs = append(errs, err)
	}

	for _, group := range groups {
		edge := group[0]

		in, err := policies.summaryInput(group, discoveries, outcomes)
		if err != nil {
			logrus.Warnf("Not writing a verification summary for %s@%s: %v", targetIdentity(&edge), edge.Digest, err)

			continue
		}

		in.Name = targetIdentity(&edge)
		in.Digest = string(edge.Digest)
		in.Version = kpromoVersion
		in.Time = now

		g.Go(func() error {
			if err := di.pushVerificationSummary(ctx, &edge, in); err != nil {
				addErr(fmt.Errorf("writing verification summary for %s: %w", in.Name, err))

				return nil
			}

			digests, err := di.unlistedChildren(ctx, &edge, promoted)
			if err != nil {
				addErr(fmt.Errorf("listing the children of %s@%s: %w", in.Name, in.Digest, err))

				return nil
			}

			mu.Lock()
			defer mu.Unlock()

			for _, digest := range digests {
				key := in.Name + "@" + digest
				if children[key] == nil {
					childEdge := edge
					childEdge.Digest = image.Digest(digest)
					childEdge.DstImageTag.Tag = ""
					children[key] = &childSummary{edge: childEdge}
				}

				children[key].indexes = append(children[key].indexes, in)
			}

			return nil
		})
	}

	_ = g.Wait() //nolint:errcheck // the errors are collected above

	// A child of several indexes gets one summary, written after all of
	// them, so that it fails when one of them fails.
	for _, key := range slices.Sorted(maps.Keys(children)) {
		child := children[key]
		slices.SortFunc(child.indexes, func(a, b *provenance.SummaryInput) int {
			return strings.Compare(a.Digest, b.Digest)
		})

		g.Go(func() error {
			in := provenance.ChildSummaryInput(string(child.edge.Digest), child.indexes)
			if err := di.pushVerificationSummary(ctx, &child.edge, in); err != nil {
				addErr(fmt.Errorf("writing verification summary for %s: %w", key, err))
			}

			return nil
		})
	}

	_ = g.Wait() //nolint:errcheck // the errors are collected above

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("writing verification summaries: %w", err)
	}

	return nil
}

// childSummary is the verification summary of a child of promoted indexes
// that is not a promoted digest of its own.
type childSummary struct {
	edge    promotion.Edge
	indexes []*provenance.SummaryInput
}

// unlistedChildren returns the digests of the children of a promoted index
// that are not promoted digests of their own, because the promoter manifest
// lists only the index. Container runtimes pull the children by digest.
// Attestation manifests of an index are no images and are left out.
func (di *DefaultPromoterImplementation) unlistedChildren(
	ctx context.Context, edge *promotion.Edge, promoted map[string]bool,
) ([]string, error) {
	canonical, err := name.NewDigest(canonicalDigestRef(edge))
	if err != nil {
		return nil, fmt.Errorf("parsing digest reference: %w", err)
	}

	desc, err := remote.Get(canonical, append(di.remoteOptions(), remote.WithContext(ctx))...)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest of %s: %w", canonical, err)
	}

	if !desc.MediaType.IsIndex() {
		return nil, nil
	}

	idx, err := desc.ImageIndex()
	if err != nil {
		return nil, fmt.Errorf("reading index %s: %w", canonical, err)
	}

	im, err := idx.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("reading index manifest %s: %w", canonical, err)
	}

	var digests []string

	for i := range im.Manifests {
		child := &im.Manifests[i]
		if !provenance.IsPlatformManifest(child) {
			continue
		}

		if digest := child.Digest.String(); !promoted[targetIdentity(edge)+"@"+digest] {
			digests = append(digests, digest)
		}
	}

	return digests, nil
}

// pushVerificationSummary signs the verification summary of an edge and
// attaches it to the digest on the canonical registry, unless the digest
// already has one.
func (di *DefaultPromoterImplementation) pushVerificationSummary(
	_ context.Context, edge *promotion.Edge, in *provenance.SummaryInput,
) error {
	dstDigestRef := canonicalDigestRef(edge)

	digest, err := name.NewDigest(dstDigestRef)
	if err != nil {
		return fmt.Errorf("parsing digest reference %s: %w", dstDigestRef, err)
	}

	exists, err := di.hasVerificationSummary(digest)
	if err != nil {
		return fmt.Errorf("checking the verification summaries of %s: %w", digest, err)
	}

	if exists {
		logrus.Debugf("Verification summary for %s already exists, skipping", dstDigestRef)

		return nil
	}

	statement, err := provenance.VerificationSummary(in)
	if err != nil {
		return fmt.Errorf("creating verification summary: %w", err)
	}

	bundleJSON, err := di.attSigner.SignStatement(statement)
	if err != nil {
		return fmt.Errorf("signing verification summary for %s: %w", digest, err)
	}

	logrus.Infof("Verification summary: pushing for %s", digest)

	remoteOpt := ociremote.WithRemoteOptions(di.remoteOptions()...)

	if err := ratelimit.WithRetry(func() error {
		return ociremote.WriteAttestationNewBundleFormat(
			digest, bundleJSON, provenance.SummaryPredicateType, remoteOpt,
		)
	}); err != nil {
		return fmt.Errorf("pushing verification summary for %s: %w", digest, err)
	}

	return nil
}

// policyContext finds the promoter manifest an edge was promoted from, the
// policy of its verification summary.
type policyContext struct {
	// manifests maps edges to the path of their promoter manifest.
	manifests map[manifestKey]string

	// images maps images to the path of their promoter manifest, for
	// digests the manifest doesn't list, like the children of an index.
	images map[manifestKey]string

	// descriptors caches the manifest descriptors by path.
	descriptors map[string]*provenance.ResourceDescriptor

	// policies are the provenance policies of the manifests, by path.
	policies map[string]*provenance.Policy
}

// newPolicyContext collects the promoter manifests of the images.
func newPolicyContext(mfests []schema.Manifest) *policyContext {
	pc := &policyContext{
		manifests:   map[manifestKey]string{},
		images:      map[manifestKey]string{},
		descriptors: map[string]*provenance.ResourceDescriptor{},
		policies:    map[string]*provenance.Policy{},
	}

	for i := range mfests {
		mfest := &mfests[i]
		if mfest.SrcRegistry == nil || mfest.Filepath == "" {
			continue
		}

		pc.policies[mfest.Filepath] = mfest.Provenance

		for _, img := range mfest.Images {
			if key := (manifestKey{src: mfest.SrcRegistry.Name, name: img.Name}); pc.images[key] == "" {
				pc.images[key] = mfest.Filepath
			}

			for digest := range img.Dmap {
				key := manifestKey{src: mfest.SrcRegistry.Name, name: img.Name, digest: digest}
				if _, exists := pc.manifests[key]; !exists {
					pc.manifests[key] = mfest.Filepath
				}
			}
		}
	}

	return pc
}

// manifest returns the path and descriptor of the promoter manifest of an
// edge, or nil if it is not known. Descriptors are built on first use.
func (pc *policyContext) manifest(key manifestKey) (string, *provenance.ResourceDescriptor) {
	path, ok := pc.manifests[key]
	if !ok {
		path, ok = pc.images[manifestKey{src: key.src, name: key.name}]
	}

	if !ok {
		return "", nil
	}

	descriptor, ok := pc.descriptors[path]
	if !ok {
		descriptor = manifestDescriptor(path)
		pc.descriptors[path] = descriptor
	}

	return path, descriptor
}

// summaryInput collects what the verification summary of a promoted
// digest is made of: every staging image it was promoted from and the
// promoter manifest. When the policies that applied are not all those of
// that one manifest, for example with nested source registries, the policy
// is the repository of the manifests at its commit.
func (pc *policyContext) summaryInput(
	group []promotion.Edge,
	discoveries map[string]*provenance.Discovery,
	outcomes map[string]*provenance.ImageProvenance,
) (*provenance.SummaryInput, error) {
	in := &provenance.SummaryInput{}
	seen := map[string]bool{}

	var (
		path       string
		descriptor *provenance.ResourceDescriptor
		ownPolicy  = true
	)

	for i := range group {
		edge := &group[i]

		ref := edge.SrcReference()
		if seen[ref] {
			continue
		}

		seen[ref] = true

		// Every promoted image went through the provenance phase, but a
		// summary must never be made of anything else.
		outcome, ok := outcomes[ref]
		if !ok || outcome == nil {
			return nil, fmt.Errorf("no provenance result for %s", ref)
		}

		in.Sources = append(in.Sources, provenance.SummarySource{Discovery: discoveries[ref], Provenance: outcome})

		edgePath, edgeDescriptor := pc.manifest(manifestKey{
			src: edge.SrcRegistry.Name, name: edge.SrcImageTag.Name, digest: edge.Digest,
		})
		if edgeDescriptor == nil {
			return nil, fmt.Errorf("no promoter manifest for %s", ref)
		}

		if descriptor == nil {
			path, descriptor = edgePath, edgeDescriptor
		} else if edgePath != path {
			ownPolicy = false
		}

		for _, policy := range outcome.Policies {
			if !policy.Equal(pc.policies[edgePath]) {
				ownPolicy = false
			}
		}
	}

	if descriptor.GetUri() == "" || descriptor.GetDigest()["gitCommit"] == "" {
		return nil, fmt.Errorf("the promoter manifest %s is not at a commit of a repository", path)
	}

	in.Policy = descriptor
	if !ownPolicy {
		in.Policy = &provenance.ResourceDescriptor{Uri: descriptor.GetUri(), Digest: descriptor.GetDigest()}
	}

	return in, nil
}

// hasVerificationSummary reports whether the promoter wrote a verification
// summary for the digest. Other summaries, for example one a build wrote,
// don't count.
func (di *DefaultPromoterImplementation) hasVerificationSummary(digest name.Digest) (bool, error) {
	refs, err := di.bundleReferrers(digest, provenance.SummaryPredicateType)
	if err != nil {
		return false, err
	}

	for _, ref := range refs {
		verifier, err := di.summaryVerifier(ref)
		if err != nil {
			logrus.Debugf("Unable to read verification summary %s: %v", ref, err)

			continue
		}

		if verifier == provenance.SummaryVerifierID {
			return true, nil
		}
	}

	return false, nil
}

// summaryVerifier returns the verifier ID of the verification summary in a
// referrer.
func (di *DefaultPromoterImplementation) summaryVerifier(ref name.Digest) (string, error) {
	img, err := remote.Image(ref, di.remoteOptions()...)
	if err != nil {
		return "", fmt.Errorf("fetching referrer: %w", err)
	}

	layers, err := img.Layers()
	if err != nil {
		return "", fmt.Errorf("reading the layers of the referrer: %w", err)
	}

	if len(layers) != 1 {
		return "", fmt.Errorf("referrer has %d layers, want 1", len(layers))
	}

	rc, err := layers[0].Uncompressed()
	if err != nil {
		return "", fmt.Errorf("reading the bundle: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, maxSummarySize))
	if err != nil {
		return "", fmt.Errorf("reading the bundle: %w", err)
	}

	envs, err := (&bundle.Parser{}).Parse(data)
	if err != nil {
		return "", fmt.Errorf("parsing the bundle: %w", err)
	}

	if len(envs) != 1 || envs[0].GetPredicate() == nil {
		return "", errors.New("bundle without a single predicate")
	}

	var predicate struct {
		Verifier struct {
			ID string `json:"id"`
		} `json:"verifier"`
	}

	if err := json.Unmarshal(envs[0].GetPredicate().GetData(), &predicate); err != nil {
		return "", fmt.Errorf("parsing the predicate: %w", err)
	}

	return predicate.Verifier.ID, nil
}
