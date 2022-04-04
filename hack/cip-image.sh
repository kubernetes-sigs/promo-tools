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

# About: This script can either build or push the cip and auditor container images. Providing the '--audit' flag
# handles test images used in the e2e auditor.

# Usage: cip-image.sh <command> [--audit]
# Commands:
#   build     docker build images from the project's Dockerfile
#   push      docker push images to their tagged location
#
# Optional Flag:
#   --audit   handle images for e2e test auditor

set -o errexit
set -o nounset
set -o pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
# Specify what group of images should be handled.
variant="cip"
# Determine what to do with the images (build or push).
operation=""

printUsage() {
    >&2 cat << EOF
Usage: $0 <command> [--audit]
Commands:
  build     docker build images from the project's Dockerfile
  push      docker push images to their tagged location

Optional Flag:
  --audit   handle images for e2e test auditor
EOF
}

# Parse runtime arguments.
for arg in "$@"; do
    case $arg in
        build|push)
            operation=$arg
            ;;
        --audit)
            variant=auditor
            ;;
        *)
            >&2 echo "ERROR: Unknown runtime argument \"$arg\""
            printUsage
            exit 1
            ;;
    esac
done

# Ensure an operation was found.
if [[ -z "${operation:-}" ]]; then
    >&2 echo "ERROR: Command not found."
    printUsage
    exit 1
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

# Either builds or pushes the image variant with provided tags.
handleVariant() {
    local variant=$1
    shift
    if [[ "$operation" == "build" ]]; then
        buildImage "$variant" "${*}"
    else
        pushImages "${*}"
    fi
}

# Entrypoint of this script.
main() {
    # Inject workspace variables.
    source <("${repo_root}"/workspace_status.sh inject)

    # Change the repo for cloudbuild jobs only.
    if [[ -n "${CLOUDBUILD_REPO:-}" ]]; then
        IMG_REPOSITORY=${CLOUDBUILD_REPO}
    fi

    # Assemble image tag prefixes.
    local tag_prefix=${IMG_REGISTRY}/${IMG_REPOSITORY}/${IMG_NAME}
    local test_tag_prefix=${TEST_AUDIT_STAGING_IMG_REPOSITORY}/${IMG_NAME}

    # NOTE: Both cip and auditor variants build an auditor image. Although they are named differently,
    # the image contents are the exact same.

    if [[ "$variant" == "auditor" ]]; then
        # Only build and push the auditor image.
        handleVariant "auditor" \
            "${test_tag_prefix}-auditor-test:latest" \
            "${test_tag_prefix}-auditor-test:latest-canary" \
            "${test_tag_prefix}-auditor-test:${IMG_TAG}" \
            "${test_tag_prefix}-auditor-test:${IMG_VERSION}"
    else
        # Build and push auditor and cip images.
        handleVariant "auditor" \
            "${tag_prefix}-auditor:latest" \
            "${tag_prefix}-auditor:latest-canary" \
            "${tag_prefix}-auditor:${IMG_TAG}" \
            "${tag_prefix}-auditor:${IMG_VERSION}"

        handleVariant "cip" \
            "${tag_prefix}:latest" \
            "${tag_prefix}:latest-canary" \
            "${tag_prefix}:${IMG_TAG}" \
            "${tag_prefix}:${IMG_VERSION}"
    fi

    >&2 echo "$0" finished.
}

main "$@"
