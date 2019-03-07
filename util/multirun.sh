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

# Run the promoter (cip) against multiple manifests, with a different service
# account credential for each. Activate the credential before invoking the
# promoter (the promoter should not have to worry about credentials --- not yet,
# at least).
#
# NOTE: This script will probably be deprecated once
# https://github.com/GoogleCloudPlatform/k8s-container-image-promoter/issues/13#issuecomment-466474754
# lands and each prod registry gets a service account defined for it.

usage()
{
    echo >&2 "usage: $0 <path/to/cip/binary> [<path/to/manifest.yaml>,<path/to/service-account.json>, ...]"
    echo >&2 "The 2nd argument onwards are '<manifest>,<service-account>' pairs."
    echo >&2
}

if (( $# < 2 )); then
    usage
    exit 1
fi

cip="$1"
shift

for opts in "$@"; do
    manifest=$(echo "$opts" | cut -d, -f1)
    service_account_creds=$(echo "$opts" | cut -d, -f2)
    activated_service_account=0

    # Authenticate as the service account. This allows the promoter to later
    # call gcloud with the flag `--account=...`. We can allow the service
    # account creds file to be empty, for testing cip locally (for the case
    # where the service account creds are already activated).
    if [[ -f "${service_account_creds}" ]]; then
        gcloud auth activate-service-account --key-file="${service_account_creds}"
        activated_service_account=1
    fi

    # Run the promoter against the manifest.
    "${cip}" -verbosity=3 -manifest="${manifest}" ${CIP_OPTS:+$CIP_OPTS}

    # As a safety measure, deactivate the service account which was activated
    # with --key-file.
    if (( activated_service_account )); then
        gcloud auth revoke
    fi
done
