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
	"errors"
	"testing"

	"sigs.k8s.io/promo-tools/v4/types/image"
)

func TestRegistryConfigFromContext(t *testing.T) {
	rc := Context{
		Name:           "gcr.io/k8s-staging-foo",
		ServiceAccount: "sa@project.iam.gserviceaccount.com",
		Src:            true,
	}

	config := RegistryConfigFromContext(rc)

	if config.Name != rc.Name {
		t.Errorf("Name = %q, want %q", config.Name, rc.Name)
	}

	if config.Src != rc.Src {
		t.Errorf("Src = %v, want %v", config.Src, rc.Src)
	}
}

func TestRegistryConfigsFromContexts(t *testing.T) {
	rcs := []Context{
		{Name: "gcr.io/staging", Src: true},
		{Name: "us-docker.pkg.dev/prod/images", Src: false},
	}

	configs := RegistryConfigsFromContexts(rcs)

	if len(configs) != 2 {
		t.Fatalf("len(configs) = %d, want 2", len(configs))
	}

	if configs[0].Name != "gcr.io/staging" {
		t.Errorf("configs[0].Name = %q, want %q", configs[0].Name, "gcr.io/staging")
	}

	if !configs[0].Src {
		t.Error("configs[0].Src = false, want true")
	}

	if configs[1].Name != "us-docker.pkg.dev/prod/images" {
		t.Errorf("configs[1].Name = %q", configs[1].Name)
	}
}

func TestNewInventory(t *testing.T) {
	inv := NewInventory()

	if inv.Images == nil {
		t.Error("Images map is nil")
	}

	if inv.MediaTypes == nil {
		t.Error("MediaTypes map is nil")
	}
}

func TestFakeProviderAddImage(t *testing.T) {
	f := NewFakeProvider()

	reg := image.Registry("gcr.io/test")
	name := image.Name("myimage")
	digest := image.Digest("sha256:abc123")
	tags := []image.Tag{"v1.0", "latest"}

	f.AddImage(reg, name, digest, tags...)

	if _, ok := f.Inventory.Images[reg]; !ok {
		t.Fatal("registry not found in inventory")
	}

	if _, ok := f.Inventory.Images[reg][name]; !ok {
		t.Fatal("image not found in inventory")
	}

	storedTags := f.Inventory.Images[reg][name][digest]
	if len(storedTags) != 2 {
		t.Fatalf("len(tags) = %d, want 2", len(storedTags))
	}
}

func TestFakeProviderReadRegistries(t *testing.T) {
	f := NewFakeProvider()
	f.AddImage("gcr.io/test", "img", "sha256:abc", "v1")

	inv, err := f.ReadRegistries(context.Background(), nil, false, nil)
	if err != nil {
		t.Fatalf("ReadRegistries() error = %v", err)
	}

	if len(inv.Images) != 1 {
		t.Errorf("len(Images) = %d, want 1", len(inv.Images))
	}
}

func TestFakeProviderReadRegistriesError(t *testing.T) {
	f := NewFakeProvider()
	f.ReadRegistriesErr = errors.New("boom")

	_, err := f.ReadRegistries(context.Background(), nil, false, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFakeProviderCopyImage(t *testing.T) {
	f := NewFakeProvider()

	err := f.CopyImage(context.Background(), "src:tag", "dst:tag")
	if err != nil {
		t.Fatalf("CopyImage() error = %v", err)
	}

	if len(f.CopiedImages) != 1 {
		t.Fatalf("len(CopiedImages) = %d, want 1", len(f.CopiedImages))
	}

	if f.CopiedImages[0].Src != "src:tag" || f.CopiedImages[0].Dst != "dst:tag" {
		t.Errorf("CopiedImages[0] = %+v", f.CopiedImages[0])
	}
}

func TestFakeProviderCopyImageError(t *testing.T) {
	f := NewFakeProvider()
	f.CopyImageErr = errors.New("copy failed")

	err := f.CopyImage(context.Background(), "src", "dst")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSplitByKnownRegistries(t *testing.T) {
	registries := []RegistryConfig{
		{Name: "gcr.io/k8s-staging-foo"},
		{Name: "us-docker.pkg.dev/k8s-artifacts-prod/images"},
	}

	tests := []struct {
		fullName    image.Registry
		wantReg     image.Registry
		wantImg     image.Name
		expectError bool
	}{
		{
			fullName: "gcr.io/k8s-staging-foo/myimage",
			wantReg:  "gcr.io/k8s-staging-foo",
			wantImg:  "myimage",
		},
		{
			fullName: "gcr.io/k8s-staging-foo/sub/path/image",
			wantReg:  "gcr.io/k8s-staging-foo",
			wantImg:  "sub/path/image",
		},
		{
			fullName: "us-docker.pkg.dev/k8s-artifacts-prod/images/kube-apiserver",
			wantReg:  "us-docker.pkg.dev/k8s-artifacts-prod/images",
			wantImg:  "kube-apiserver",
		},
		{
			fullName:    "unknown.registry.io/foo",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.fullName), func(t *testing.T) {
			reg, img, err := splitByKnownRegistries(tt.fullName, registries)
			if tt.expectError {
				if err == nil {
					t.Error("expected error")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if reg != tt.wantReg {
				t.Errorf("reg = %q, want %q", reg, tt.wantReg)
			}

			if img != tt.wantImg {
				t.Errorf("img = %q, want %q", img, tt.wantImg)
			}
		})
	}
}

func TestSupportedMediaType(t *testing.T) {
	tests := []struct {
		input     string
		expectErr bool
	}{
		{"application/vnd.docker.distribution.manifest.v2+json", false},
		{"application/vnd.docker.distribution.manifest.list.v2+json", false},
		{"application/vnd.oci.image.manifest.v1+json", false},
		{"application/vnd.oci.image.index.v1+json", false},
		{"application/vnd.docker.distribution.manifest.v1+json", false},
		{"application/vnd.docker.distribution.manifest.v1+prettyjws", false},
		{"", false}, // empty defaults to DockerManifestSchema2
		{"application/vnd.unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := supportedMediaType(tt.input)
			if tt.expectErr && err == nil {
				t.Error("expected error")
			}

			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
