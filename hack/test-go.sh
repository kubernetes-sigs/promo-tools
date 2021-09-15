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

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)

# Default timeout is 1800s
TEST_TIMEOUT=${TIMEOUT:-1800}

# Write go test artifacts here
ARTIFACTS=${ARTIFACTS:-"${REPO_ROOT}/tmp"}

for arg in "$@"
do
    case $arg in
        -t=*|--timeout=*)
        TEST_TIMEOUT="${arg#*=}"
        shift
        ;;
        -t|--timeout)
        TEST_TIMEOUT="$2"
        shift
        shift
        ;;
        --failfast)
        TEST_FAILFAST="-failfast"
        shift
    esac
done

cd "${REPO_ROOT}"

mkdir -p "${ARTIFACTS}"

go_test_flags=(
    -v
    -count=1
    -timeout="${TEST_TIMEOUT}s"
    -cover -coverprofile "${ARTIFACTS}/coverage.out"
    ${TEST_FAILFAST:-}
)

packages=()
mapfile -t packages < <(go list ./... | grep -v 'sigs.k8s.io/promo-tools/cmd\|test-e2e')

export GO111MODULE=on

# If the user rquests failing fast (so that they can iterate on the failing test
# quickly without having to swim across the verbose log outputs from parallel
# tests), then iterate through each module serially.
if [[ -n "${TEST_FAILFAST:-}" ]]; then
    echo >&2 "Testing serially with -failfast"
    echo >&2 "${go_test_flags[@]}"
    for package in "${packages[@]}"; do
        go test "${go_test_flags[@]}" "${package}"
    done
else
    # By default, we run tests in parallel. This is used in CI.
    go test "${go_test_flags[@]}" "${packages[@]}"
fi

go tool cover -html "${ARTIFACTS}/coverage.out" -o "${ARTIFACTS}/coverage.html"
