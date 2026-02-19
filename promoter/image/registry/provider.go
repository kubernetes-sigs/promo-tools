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

	cr "github.com/google/go-containerregistry/pkg/v1/types"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

// Provider defines operations for interacting with container image registries.
//
//counterfeiter:generate . Provider
type Provider interface {
	// ReadRegistries reads the image inventory from one or more registries.
	// If recurse is true, child repositories are walked recursively.
	ReadRegistries(ctx context.Context, registries []RegistryConfig, recurse bool) (*Inventory, error)

	// CopyImage copies a container image from the source reference to the
	// destination reference. References can be by digest (FQIN) or tag (PQIN).
	CopyImage(ctx context.Context, src, dst string) error
}

// RegistryConfig describes a container image registry endpoint.
type RegistryConfig struct {
	// Name is the registry URL (e.g., "us-docker.pkg.dev/k8s-artifacts-prod/images").
	Name image.Registry

	// ServiceAccount is the service account used for authentication.
	ServiceAccount string

	// Src marks this registry as a source (staging) registry.
	Src bool
}

// RegistryConfigFromContext converts a legacy registry.Context to a RegistryConfig.
func RegistryConfigFromContext(rc Context) RegistryConfig {
	return RegistryConfig{
		Name:           rc.Name,
		ServiceAccount: rc.ServiceAccount,
		Src:            rc.Src,
	}
}

// RegistryConfigsFromContexts converts a slice of legacy registry.Context to RegistryConfigs.
func RegistryConfigsFromContexts(rcs []Context) []RegistryConfig {
	configs := make([]RegistryConfig, len(rcs))
	for i, rc := range rcs {
		configs[i] = RegistryConfigFromContext(rc)
	}
	return configs
}

// Inventory holds the results of reading registry inventories.
type Inventory struct {
	// Images maps registry names to their image inventories.
	Images map[image.Registry]RegInvImage

	// MediaTypes maps digests to their OCI media types.
	MediaTypes map[image.Digest]cr.MediaType
}

// NewInventory creates an empty Inventory.
func NewInventory() *Inventory {
	return &Inventory{
		Images:     make(map[image.Registry]RegInvImage),
		MediaTypes: make(map[image.Digest]cr.MediaType),
	}
}
