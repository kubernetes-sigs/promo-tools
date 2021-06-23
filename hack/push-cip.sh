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

# About: This script builds and pushes the cip and auditor images to a public repository,
# defined in workspace_status.sh.

set -o errexit
set -o nounset
set -o pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
# Detect which container variant to build.
build_variant="cip"

printUsage() {
    >&2 echo "Usage: $0 [--audit]"
}

if (( $# > 1 )) || { (( $# > 0 )) && [[ "$1" != --audit ]];}; then
    >&2 echo "ERROR: Invalid arguments."
    printUsage
    exit 1
elif (( $# == 1 )) && [[ "$1" == --audit ]]; then
    build_variant="auditor"
fi

# Takes array of images and pushes to their tagged registry location.
pushImages() {
    local images="${*}"
    for img in $images; do
        docker push "$img"
    done
}

# Builds either auditor or cip container image from repo_root. Also adds
# tags, given by an array of tags.
buildImage() {
    # Add build variant argument.
    local variant
    if [[ "$1" == "auditor" ]]; then
        variant="test"
    else
        variant="prod"
    fi 
    local cmd="docker build --build-arg variant=$variant "
    # Concatenate tags.
    shift
    local tags="${*}"
    for tag in $tags; do
        cmd+="-t $tag "
    done
    # Specify Dockerfile location.
    cmd+="$repo_root"
    # Build the container.
    >&2 echo "Executing: $cmd"
    $cmd
}

# Builds and pushes the image variant with specified tags.
shipImage() {
    local variant=$1
    shift
    buildImage "$variant" "${*}"
    pushImages "${*}"
}

# Entrypoint of this script.
main() {
    # Inject workspace variables.
    source <("${repo_root}"/workspace_status.sh inject)

    # Change the repo for cloudbuild jobs only.
    if [[ -n "${CLOUDBUILD_REPO:-}" ]]; then
        STABLE_IMG_REPOSITORY=${CLOUDBUILD_REPO}
    fi

    # Assemble image tag prefixes.
    local stable_tag_prefix=${STABLE_IMG_REGISTRY}/${STABLE_IMG_REPOSITORY}/${STABLE_IMG_NAME}
    local test_tag_prefix=${STABLE_TEST_AUDIT_STAGING_IMG_REPOSITORY}/${STABLE_IMG_NAME}

    # NOTE: Both cip and auditor variants build an auditor image. Although they are named differently,
    # the image contents are the exact same.

    if [[ "$build_variant" == "auditor" ]]; then
        # Only build and push the auditor image.
        shipImage "auditor" \
            "${test_tag_prefix}-auditor-test:latest" \
            "${test_tag_prefix}-auditor-test:${STABLE_IMG_TAG}"
    else
        # Build and push auditor and cip images.
        shipImage "auditor" \
            "${stable_tag_prefix}-auditor:latest" \
            "${stable_tag_prefix}-auditor:${STABLE_IMG_TAG}"

        shipImage "cip" \
            "${stable_tag_prefix}:latest" \
            "${stable_tag_prefix}:${STABLE_IMG_TAG}"
    fi

    >&2 echo "$0" finished.
}

main "$@"
