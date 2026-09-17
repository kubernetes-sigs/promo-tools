#!/usr/bin/env bash

# Copyright 2026 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

BUF_VERSION=v1.72.0

REPO_ROOT=$(git rev-parse --show-toplevel)
BIN_DIR=$(mktemp -d)
trap 'rm -rf "${BIN_DIR}"' EXIT

cd "${REPO_ROOT}"

# protoc-gen-go follows the google.golang.org/protobuf version in go.mod.
go build -o "${BIN_DIR}/protoc-gen-go" google.golang.org/protobuf/cmd/protoc-gen-go
GOBIN="${BIN_DIR}" go install "github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}"

PATH="${BIN_DIR}:${PATH}" buf generate
