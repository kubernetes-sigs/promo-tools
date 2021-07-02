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

# About: This script tests the build process of the cip-image.sh script for both
# variants (cip and auditor).

set -o errexit
set -o nounset
set -o pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)

# Ensures the provided images are built locally.
validateImages() {
    local images="${*}"
    local img_id
    # Look for every image locally.
    for img in $images; do
        # Obtain the image ID.
        img_id=$(docker image ls "$img" --format '{{.ID}}')
        # Ensure the image exists.
        if [[ -z "$img_id" ]]; then
            >&2 echo "ERROR: Image \"$img\" was not found locally."
            exit 1
        fi
    done
}

# Validate the container build process for the given variant.
testVariant() {
    # Build based on the variant.
    if [[ "$1" == "auditor" ]]; then
        make image-build-cip-auditor-e2e
    else
        make image-build
    fi

    # Check required images are built locally.
    shift
    validateImages "${*}"
}

# Program entrypoint.
main() {
    # Inject workspace variables.
    source <("${repo_root}"/workspace_status.sh inject)

    # Assemble image tag prefixes.
    local stable_tag_prefix=${STABLE_IMG_REGISTRY}/${STABLE_IMG_REPOSITORY}/${STABLE_IMG_NAME}
    local test_tag_prefix=${STABLE_TEST_AUDIT_STAGING_IMG_REPOSITORY}/${STABLE_IMG_NAME}

    # Test each variant.
    testVariant "cip" \
        "${stable_tag_prefix}-auditor:latest" \
        "${stable_tag_prefix}-auditor:${STABLE_IMG_TAG}" \
        "${stable_tag_prefix}:latest" \
        "${stable_tag_prefix}:${STABLE_IMG_TAG}"

    testVariant "auditor" \
        "${test_tag_prefix}-auditor-test:latest" \
        "${test_tag_prefix}-auditor-test:${STABLE_IMG_TAG}"

    >&2 echo "$0" finished.
}

main
