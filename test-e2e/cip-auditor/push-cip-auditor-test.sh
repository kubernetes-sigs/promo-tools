#!/usr/bin/env bash

# Copyright 2020 The Kubernetes Authors.
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

# About: This script pushes auditor test images to the auditor's staging
# location (defined in workspace_status.sh) for e2e tests.

set -o errexit
set -o nounset
set -o pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)

# Inject workspace variables.
source <("${repo_root}"/workspace_status.sh inject)

# Build docker container from Dockerfile.
output=$(docker build "$repo_root" | tail -1)
# Grab only the container ID. Expected to transform a line like
# "Successfully built 703827382376" into "703827382376".
container_id=$(echo "$output" | cut -d ' ' -f 3)
# Asesmble image names.
latest="${STABLE_TEST_AUDIT_STAGING_IMG_REPOSITORY}/${STABLE_IMG_NAME}-auditor-test:latest"
stable="${STABLE_TEST_AUDIT_STAGING_IMG_REPOSITORY}/${STABLE_IMG_NAME}-auditor-test:${STABLE_IMG_TAG}"
# Assign additional tags to the image.
docker tag "$container_id" "$latest"
docker tag "$container_id" "$stable"
# Push images.
docker push "$latest"
docker push "$stable"

echo "$0" finished.
