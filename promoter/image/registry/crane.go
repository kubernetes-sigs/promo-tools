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

package registry

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/gcrane"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrV1Google "github.com/google/go-containerregistry/pkg/v1/google"
	cr "github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"sigs.k8s.io/promo-tools/v4/promoter/image/ratelimit"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// readRegistryConcurrency controls the maximum number of concurrent
// registry read operations. Each read is an HTTP request bounded by
// the rate limiter at the transport level.
const readRegistryConcurrency = 20

// CraneProvider implements Provider using go-containerregistry/crane and the
// Google-specific extensions for optimized registry walking.
type CraneProvider struct {
	transport http.RoundTripper
	craneOpts []crane.Option
}

// CraneOption configures a CraneProvider.
type CraneOption func(*CraneProvider)

// WithTransport sets the HTTP transport for registry operations.
func WithTransport(rt http.RoundTripper) CraneOption {
	return func(p *CraneProvider) {
		p.transport = rt
	}
}

// WithCraneOptions sets additional crane options for registry operations.
// This can be used to pass options like crane.Insecure for non-TLS registries.
func WithCraneOptions(opts ...crane.Option) CraneOption {
	return func(p *CraneProvider) {
		p.craneOpts = opts
	}
}

// NewCraneProvider creates a new CraneProvider with the given options.
func NewCraneProvider(opts ...CraneOption) *CraneProvider {
	p := &CraneProvider{}
	for _, o := range opts {
		o(p)
	}

	return p
}

// ReadRegistries reads the image inventory from one or more registries using
// google.Walk (recursive) or google.List (non-recursive).
// baseRegistries, when non-nil, provides the base registry paths used to
// key the returned inventory. This is needed when registries contain
// full image paths (e.g. "gcr.io/staging/image") but the inventory must
// be keyed by the base registry (e.g. "gcr.io/staging").
func (p *CraneProvider) ReadRegistries(
	ctx context.Context, registries []RegistryConfig, recurse bool, baseRegistries []RegistryConfig,
) (*Inventory, error) {
	logrus.Infof("Reading %d registries (recursive: %v)", len(registries), recurse)

	inv := NewInventory()

	var mu sync.Mutex

	// Use base registries for splitting repo paths into registry+image
	// name. When not provided, fall back to the registries parameter.
	splitRegs := baseRegistries
	if len(splitRegs) == 0 {
		splitRegs = registries
	}

	total := len(registries)

	var completed atomic.Int64

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(readRegistryConcurrency)

	for _, r := range registries {
		g.Go(func() error {
			repo, err := name.NewRepository(string(r.Name))
			if err != nil {
				return fmt.Errorf("parsing repo name %s: %w", r.Name, err)
			}

			walkOpts := []ggcrV1Google.Option{
				ggcrV1Google.WithAuthFromKeychain(gcrane.Keychain),
				ggcrV1Google.WithContext(gctx),
			}

			recordTags := makeTagRecorder(inv, &mu, splitRegs)

			if recurse {
				if err := ratelimit.WithRetry(func() error {
					return ggcrV1Google.Walk(repo, recordTags, walkOpts...)
				}); err != nil {
					return fmt.Errorf("walking repo %s: %w", r.Name, err)
				}
			} else {
				var tags *ggcrV1Google.Tags

				if err := ratelimit.WithRetry(func() error {
					var listErr error

					tags, listErr = ggcrV1Google.List(repo, walkOpts...)
					if listErr != nil {
						return fmt.Errorf("listing: %w", listErr)
					}

					return nil
				}); err != nil {
					return fmt.Errorf("listing repo %s: %w", r.Name, err)
				}

				if err := recordTags(repo, tags, nil); err != nil {
					return fmt.Errorf("recording tags for %s: %w", r.Name, err)
				}
			}

			logrus.Infof("Read registry %d/%d: %s", completed.Add(1), total, r.Name)

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("reading registries: %w", err)
	}

	return inv, nil
}

// CopyImage copies a container image from src to dst using crane.
func (p *CraneProvider) CopyImage(_ context.Context, src, dst string) error {
	opts := []crane.Option{
		crane.WithAuthFromKeychain(gcrane.Keychain),
		crane.WithUserAgent(image.UserAgent),
	}
	if p.transport != nil {
		opts = append(opts, crane.WithTransport(p.transport))
	}

	opts = append(opts, p.craneOpts...)

	if err := crane.Copy(src, dst, opts...); err != nil {
		return fmt.Errorf("copying image %s to %s: %w", src, dst, err)
	}

	return nil
}

// makeTagRecorder creates a callback function for google.Walk that records
// found tags into the inventory.
func makeTagRecorder(
	inv *Inventory, mu *sync.Mutex, registries []RegistryConfig,
) func(name.Repository, *ggcrV1Google.Tags, error) error {
	return func(repo name.Repository, tags *ggcrV1Google.Tags, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walking registry: %w", walkErr)
		}

		regName, imageName, err := splitByKnownRegistries(
			image.Registry(repo.Name()), registries,
		)
		if err != nil {
			return fmt.Errorf("splitting repo and image name: %w", err)
		}

		logrus.Debugf("Registry: %s Image: %s Got: %s", regName, imageName, repo.Name())

		digestTags := make(DigestTags)

		if tags != nil && tags.Manifests != nil {
			for digest, manifest := range tags.Manifests {
				tagSlice := TagSlice{}
				for _, tag := range manifest.Tags {
					tagSlice = append(tagSlice, image.Tag(tag))
				}

				digestTags[image.Digest(digest)] = tagSlice

				mediaType, err := supportedMediaType(manifest.MediaType)
				if err != nil {
					logrus.Errorf("Processing digest %s: %v", digest, err)
				}

				mu.Lock()
				inv.MediaTypes[image.Digest(digest)] = mediaType
				mu.Unlock()
			}
		}

		logrus.Debugf("%d tags found", len(digestTags))

		mu.Lock()
		if _, ok := inv.Images[regName]; !ok {
			inv.Images[regName] = make(RegInvImage)
		}

		if len(digestTags) > 0 {
			inv.Images[regName][imageName] = digestTags
		}
		mu.Unlock()

		return nil
	}
}

// splitByKnownRegistries splits a full image path into the registry name
// and image name components based on known registries.
func splitByKnownRegistries(
	fullName image.Registry, registries []RegistryConfig,
) (image.Registry, image.Name, error) {
	for _, r := range registries {
		rn := string(r.Name)

		fn := string(fullName)
		if len(fn) > len(rn) && fn[:len(rn)] == rn && fn[len(rn)] == '/' {
			return r.Name, image.Name(fn[len(rn)+1:]), nil
		}

		if fn == rn {
			return r.Name, "", nil
		}
	}

	return "", "", fmt.Errorf("could not determine registry for %s", fullName)
}

// supportedMediaType returns the appropriate MediaType, or an error if
// the media type is not supported.
func supportedMediaType(mediaType string) (cr.MediaType, error) {
	switch cr.MediaType(mediaType) {
	case cr.DockerManifestSchema2:
		return cr.DockerManifestSchema2, nil
	case cr.DockerManifestList:
		return cr.DockerManifestList, nil
	case cr.OCIManifestSchema1:
		return cr.OCIManifestSchema1, nil
	case cr.OCIImageIndex:
		return cr.OCIImageIndex, nil
	case cr.DockerManifestSchema1, cr.DockerManifestSchema1Signed:
		return cr.MediaType(mediaType), nil
	default:
		// Default to DockerManifestSchema2 for backwards compatibility.
		if mediaType == "" {
			return cr.DockerManifestSchema2, nil
		}

		return cr.MediaType(mediaType),
			fmt.Errorf("unsupported media type %q", mediaType)
	}
}
