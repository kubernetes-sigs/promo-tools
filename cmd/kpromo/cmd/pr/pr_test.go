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

package pr

import (
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/release-sdk/github"
)

const (
	testFork = "cpanato"
	testTag  = "v1.34.0"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name        string
		opts        promoteOptions
		expectedErr bool
	}{
		{
			name: "valid --- no issue",
			opts: promoteOptions{
				userFork: testFork,
				tags:     []string{testTag},
			},
		},
		{
			name: "valid --- https issue URL",
			opts: promoteOptions{
				userFork: testFork,
				tags:     []string{testTag},
				issue:    "https://github.com/kubernetes/k8s.io/issues/1234",
			},
		},
		{
			name: "valid --- http issue URL",
			opts: promoteOptions{
				userFork: testFork,
				tags:     []string{testTag},
				issue:    "http://github.com/kubernetes/k8s.io/issues/1234",
			},
		},
		{
			name: "invalid --- issue URL without scheme",
			opts: promoteOptions{
				userFork: testFork,
				tags:     []string{testTag},
				issue:    "github.com/kubernetes/k8s.io/issues/1234",
			},
			expectedErr: true,
		},
		{
			name: "invalid --- issue URL with unsupported scheme",
			opts: promoteOptions{
				userFork: testFork,
				tags:     []string{testTag},
				issue:    "ftp://github.com/kubernetes/k8s.io/issues/1234",
			},
			expectedErr: true,
		},
		{
			name: "invalid --- issue URL without host",
			opts: promoteOptions{
				userFork: testFork,
				tags:     []string{testTag},
				issue:    "https://",
			},
			expectedErr: true,
		},
		{
			name: "invalid --- issue URL on another host",
			opts: promoteOptions{
				userFork: testFork,
				tags:     []string{testTag},
				issue:    "https://github.example.com/kubernetes/k8s.io/issues/1234",
			},
			expectedErr: true,
		},
		{
			name: "invalid --- no tags",
			opts: promoteOptions{
				userFork: testFork,
			},
			expectedErr: true,
		},
		{
			name: "invalid --- no fork",
			opts: promoteOptions{
				tags: []string{testTag},
			},
			expectedErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(github.TokenEnvKey, "ghp-testing")

			err := tc.opts.Validate()
			if tc.expectedErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestValidateRequiresGitHubToken(t *testing.T) {
	t.Setenv(github.TokenEnvKey, "")

	opts := promoteOptions{
		userFork: testFork,
		tags:     []string{testTag},
	}

	require.Error(t, opts.Validate())
}

func TestGeneratePRBody(t *testing.T) {
	tests := []struct {
		name     string
		opts     promoteOptions
		expected string
	}{
		{
			name: "without issue",
			opts: promoteOptions{
				userFork:  testFork,
				project:   defaultProject,
				reviewers: defaultReviewers,
				tags:      []string{testTag},
			},
			expected: "Image promotion for kubernetes v1.34.0\n" +
				"This is an automated PR generated from `kpromo`\n" +
				"```\nkpromo pr --fork cpanato --tag v1.34.0\n```\n\n" +
				"/hold\ncc: @kubernetes/release-engineering\n",
		},
		{
			name: "with issue",
			opts: promoteOptions{
				userFork:  testFork,
				project:   defaultProject,
				reviewers: defaultReviewers,
				tags:      []string{testTag},
				issue:     "https://github.com/kubernetes/k8s.io/issues/1234",
			},
			expected: "Image promotion for kubernetes v1.34.0\n" +
				"\nxref: https://github.com/kubernetes/k8s.io/issues/1234\n\n" +
				"This is an automated PR generated from `kpromo`\n" +
				"```\nkpromo pr --fork cpanato --issue \"https://github.com/kubernetes/k8s.io/issues/1234\" --tag v1.34.0\n```\n\n" +
				"/hold\ncc: @kubernetes/release-engineering\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, generatePRBody(&tc.opts))
		})
	}
}
