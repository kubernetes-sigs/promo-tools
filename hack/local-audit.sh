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

# About: This script runs the auditor locally in --verbose mode.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)

# Print the usage for this script.
usage() {
    echo >&2 "Usage: $0 <SERVICE_ACCOUNT_KEY_FILE>"
    exit 1
}

# Program entrypoint.
main() {
    # Ensure correct number of runtime params.
    if [[ $# != 1 ]]; then
        echo >&2 "Key-file not found."
        usage
    fi

    # Define the service account key-file.
    export GOOGLE_APPLICATION_CREDENTIALS=$1

    # Build CIP binary.
    pushd "${REPO_ROOT}"
    make build
    popd

    # Setup runtime arguments.
    local manifest_repo_url
    local manifest_repo_branch
    local manifest_repo_dir
    local gcp_project_id
    
    # Use default value if environment variable is not set.
    manifest_repo_url="${CIP_AUDIT_MANIFEST_REPO_URL:-https://github.com/kubernetes/k8s.io}"    
    manifest_repo_branch="${CIP_AUDIT_MANIFEST_REPO_BRANCH:-main}"
    manifest_repo_dir="${CIP_AUDIT_MANIFEST_REPO_MANIFEST_DIR:-k8s.gcr.io}"
    gcp_project_id="${CIP_AUDIT_GCP_PROJECT_ID:-k8s-artifacts-prod}"

    # Start the auditor.
    "${REPO_ROOT}/cip" audit \
        --branch="$manifest_repo_branch" \
        --path="$manifest_repo_dir" \
        --project="$gcp_project_id" \
        --url="$manifest_repo_url" \
        --verbose
}

main "$@"
