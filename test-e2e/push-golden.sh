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

# About: This script automatest the process of pushing golden iamges for e2e tests.
# Images are loaded from local archives and pushed to the designated staging repo.
# The script is triggered within e2e.go during test setup.
#
# Usage:
#   ./push-golden.sh repo-root

set -o errexit
set -o nounset
set -o pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
archive_path="${repo_root}/test-e2e/cip/golden-archives"
source <(${repo_root}/workspace_status.sh inject)

echo $STABLE_IMG_REGISTRY

# Load archives.
docker load -i "${archive_path}/bar/1.0.tar"
docker load -i "${archive_path}/foo/1.0-linux_amd64.tar"
docker load -i "${archive_path}/foo/1.0-linux_s390x.tar"
docker load -i "${archive_path}/foo/NOTAG-0.tar"

# Push to k8s-staging-cip-test.
docker push "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-bar/bar:1.0"
docker push "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0-linux_amd64"
docker push "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0-linux_s390x"
docker push "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:NOTAG-0"

# Create a manifest.
docker manifest create \
    "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0" \
    "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0-linux_amd64" \
    "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0-linux_s390x"

# Fixup the s390x image because it's set to amd64 by default (there is
# no way to specify architecture from within bazel yet when creating
# images).
docker manifest annotate --arch=s390x \
    "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0" \
    "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0-linux_s390x"

# Show manifest for debugging.
docker manifest inspect "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0"

# Push the manifest list.
docker manifest push --purge "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:1.0"

# Remove tag for tagless image.
gcloud container images untag --quiet "${STABLE_TEST_STAGING_IMG_REPOSITORY}/golden-foo/foo:NOTAG-0"
