#!/bin/bash

# Copyright 2019 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

shopt -s dotglob
# Remove the git archive that GCB auto-injects to /workspace, because it's
# not a real Git repository (it's just a tarball). This is required because
# we want to make use of git commands such as git-describe later on.
rm -rf /workspace/*

git \
  clone https://sigs.k8s.io/${_REPO} \
  --depth 1 \
  --branch $BRANCH_NAME \
  ${_GOPATH}/src/sigs.k8s.io/${_REPO}

# Get golangci-lint; install into /workspace/bin/golangci-lint.
pushd go
curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh \
  | sh -s ${_GOLANGCI_LINT_VERSION}