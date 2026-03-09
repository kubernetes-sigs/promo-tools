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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionsValidate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		opts      Options
		shouldErr bool
	}{
		{
			name:      "manifest set",
			opts:      Options{Manifest: "path/to/manifest.yaml"},
			shouldErr: false,
		},
		{
			name:      "thin manifest dir set",
			opts:      Options{ThinManifestDir: "path/to/dir"},
			shouldErr: false,
		},
		{
			name:      "snapshot set",
			opts:      Options{Snapshot: "gcr.io/test"},
			shouldErr: false,
		},
		{
			name:      "manifest based snapshot set",
			opts:      Options{ManifestBasedSnapshotOf: "gcr.io/test"},
			shouldErr: false,
		},
		{
			name:      "snapshot bypasses manifest check",
			opts:      Options{Snapshot: "gcr.io/test"},
			shouldErr: false,
		},
		{
			name:      "nothing set",
			opts:      Options{},
			shouldErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.Validate()
			if tc.shouldErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
