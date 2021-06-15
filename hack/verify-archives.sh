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

# About:
# This script extracts the golden image tarballs and compares their contents against the
# already-tracked golden folder (which is used by Bazel to generate deterministic images
# on every e2e test invocation). The purpose of this script is to verify that the tarballs
# residing in golden-archives matches the original source of truth (golden).
#
# Usage:
#   verify-archives.sh repo-root
#

set -o errexit
set -o nounset
set -o pipefail

repoRoot=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)

# Mapping for each golden image KEY=localPath VALUE=containerPath.
declare -A paths
paths[bar/1.0]=bar/1.0
paths[foo/1.0-linux_amd64]=foo/linux_amd64/1.0-linux_amd64
paths[foo/1.0-linux_s390x]=foo/linux_s390x/1.0-linux_s390x
paths[foo/NOTAG-0]=foo/NOTAG/NOTAG-0

# Setup mock directory structure.
mockGolden=$(mktemp -d)
mkdir "${mockGolden}/foo"
mkdir "${mockGolden}/bar"

goldenArchive="${repoRoot}/test-e2e/cip/golden-archives/"
goldenContent="${repoRoot}/test-e2e/cip/golden"

# Extracts data file from each container image.
for path in "${!paths[@]}"
do
    containerPath=${paths[$path]}
    # Get parent directory of file.
    goldenParent=${path%%/*}
    # Load image from tarball archive.
    output=$(docker load -i "${goldenArchive}${path}.tar")
    # Reformat docker load output to only grab the image ID.
    if [[ "$output" =~ sha256:(.*) ]]
    then
        img="${BASH_REMATCH[1]}"
    else
            echo "Error: failed to get image digest from docker load output"
            exit 1
    fi
    # Save container ID after creation.
    id=$(docker create "$img" /dev/null)
    # Specify the absolute path to the data file within the container.
    containerAbsPath="/golden/${containerPath}"
    # Copy data file from inside container to temporary directory.
    docker cp "$id":"$containerAbsPath" "$mockGolden/${goldenParent}"
done

# Ensure mock-golden is an exact copy of test-e2e/cip/golden.
diff -r "$mockGolden" "$goldenContent"

# Cleanup
rm -rf "$mockGolden"
