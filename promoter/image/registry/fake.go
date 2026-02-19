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
	"sync"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

// FakeProvider is an in-memory implementation of Provider for testing.
type FakeProvider struct {
	mu sync.Mutex

	// Inventory is the pre-populated inventory returned by ReadRegistries.
	Inventory *Inventory

	// CopiedImages records all src->dst copy calls.
	CopiedImages []CopyRecord

	// ReadRegistriesErr forces ReadRegistries to return this error.
	ReadRegistriesErr error

	// CopyImageErr forces CopyImage to return this error.
	CopyImageErr error
}

// CopyRecord records the arguments to a CopyImage call.
type CopyRecord struct {
	Src, Dst string
}

// NewFakeProvider creates a FakeProvider with an empty inventory.
func NewFakeProvider() *FakeProvider {
	return &FakeProvider{
		Inventory: NewInventory(),
	}
}

// AddImage adds an image to the fake inventory.
func (f *FakeProvider) AddImage(
	reg image.Registry, name image.Name,
	digest image.Digest, tags ...image.Tag,
) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.Inventory.Images[reg]; !ok {
		f.Inventory.Images[reg] = make(RegInvImage)
	}
	if _, ok := f.Inventory.Images[reg][name]; !ok {
		f.Inventory.Images[reg][name] = make(DigestTags)
	}
	f.Inventory.Images[reg][name][digest] = tags
}

// ReadRegistries returns the pre-populated inventory.
func (f *FakeProvider) ReadRegistries(
	_ context.Context, _ []RegistryConfig, _ bool,
) (*Inventory, error) {
	if f.ReadRegistriesErr != nil {
		return nil, f.ReadRegistriesErr
	}
	return f.Inventory, nil
}

// CopyImage records the copy and returns the configured error.
func (f *FakeProvider) CopyImage(_ context.Context, src, dst string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.CopiedImages = append(f.CopiedImages, CopyRecord{Src: src, Dst: dst})
	if f.CopyImageErr != nil {
		return fmt.Errorf("fake copy error: %w", f.CopyImageErr)
	}
	return nil
}
