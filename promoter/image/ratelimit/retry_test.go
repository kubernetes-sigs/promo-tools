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

package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stretchr/testify/assert"
)

func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "context deadline exceeded",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "wrapped context deadline exceeded",
			err:  fmt.Errorf("listing tags: %w", context.DeadlineExceeded),
			want: true,
		},
		{
			name: "429 too many requests",
			err:  &transport.Error{StatusCode: http.StatusTooManyRequests},
			want: true,
		},
		{
			name: "500 internal server error",
			err:  &transport.Error{StatusCode: http.StatusInternalServerError},
			want: true,
		},
		{
			name: "503 service unavailable",
			err:  &transport.Error{StatusCode: http.StatusServiceUnavailable},
			want: true,
		},
		{
			name: "404 not found",
			err:  &transport.Error{StatusCode: http.StatusNotFound},
			want: false,
		},
		{
			name: "401 unauthorized",
			err:  &transport.Error{StatusCode: http.StatusUnauthorized},
			want: false,
		},
		{
			name: "generic error",
			err:  errors.New("something went wrong"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				// IsTransient should not panic on nil, but the caller
				// should never pass nil in practice.
				return
			}

			assert.Equal(t, tt.want, IsTransient(tt.err))
		})
	}
}
