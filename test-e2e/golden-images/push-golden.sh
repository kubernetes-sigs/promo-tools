#!/usr/bin/env bash

# Copyright 2021 The Kubernetes Authors.
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

# About: This script automatest the process of pushing golden iamges for both e2e tests.
# Images are loaded from local archives and pushed to the designated staging repo. When
# passed the --audit flag, images will be tagged and pushed to
# STABLE_TEST_AUDIT_STAGING_IMG_REPOSITORY, otherwise defaulting to
# STABLE_TEST_STAGING_IMG_REPOSITORY (both defined in workspace_status.sh).
#
# Usage:
#   ./push-golden.sh [--audit]

set -o errexit
set -o nounset
set -o pipefail

printUsage() {
    >&2 echo "Usage: $0 [--audit]"
}

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
archive_path="${repo_root}/test-e2e/golden-images/archives"
# Inject workspace variables
source <(${repo_root}/workspace_status.sh inject)
staging_repo="$STABLE_TEST_STAGING_IMG_REPOSITORY"

if [[ $# == 1 ]]; then
    if [[ "$1" == --audit ]]; then
        staging_repo="$STABLE_TEST_AUDIT_STAGING_IMG_REPOSITORY"
    else
        >&2 echo "ERROR: Malformed flag!"
        printUsage
        exit 1
    fi
elif (( $# > 1 )); then
    >&2 echo "ERROR: Invalid number of arguments!"
    printUsage
    exit 1
fi

# Load archives.
docker load -i "${archive_path}/bar/1.0.tar"
docker load -i "${archive_path}/foo/1.0-linux_amd64.tar"
docker load -i "${archive_path}/foo/1.0-linux_s390x.tar"
docker load -i "${archive_path}/foo/NOTAG-0.tar"

# Re-tag images (only for auditor)
if [[ "$staging_repo" == "$STABLE_TEST_AUDIT_STAGING_IMG_REPOSITORY" ]]; then
    docker tag "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-bar/bar:1.0" "${staging_repo}/golden-bar/bar:1.0"
    docker tag "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0-linux_amd64" "${staging_repo}/golden-foo/foo:1.0-linux_amd64"
    docker tag "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0-linux_s390x" "${staging_repo}/golden-foo/foo:1.0-linux_s390x"
    docker tag "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:NOTAG-0" "${staging_repo}/golden-foo/foo:NOTAG-0"
fi

# Push to k8s-staging-cip-test.
docker push "${staging_repo}/golden-bar/bar:1.0"
docker push "${staging_repo}/golden-foo/foo:1.0-linux_amd64"
docker push "${staging_repo}/golden-foo/foo:1.0-linux_s390x"
docker push "${staging_repo}/golden-foo/foo:NOTAG-0"

# Create a manifest.
docker manifest create \
    "${staging_repo}/golden-foo/foo:1.0" \
    "${staging_repo}/golden-foo/foo:1.0-linux_amd64" \
    "${staging_repo}/golden-foo/foo:1.0-linux_s390x"

# Fixup the s390x image because it's set to amd64 by default (there is
# no way to specify architecture from within bazel yet when creating
# images).
docker manifest annotate --arch=s390x \
    "${staging_repo}/golden-foo/foo:1.0" \
    "${staging_repo}/golden-foo/foo:1.0-linux_s390x"

# Show manifest for debugging.
docker manifest inspect "${staging_repo}/golden-foo/foo:1.0"

# Push the manifest list.
docker manifest push --purge "${staging_repo}/golden-foo/foo:1.0"

# Remove tag for tagless image.
gcloud container images untag --quiet "${staging_repo}/golden-foo/foo:NOTAG-0"
