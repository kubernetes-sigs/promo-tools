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

# Run the promoter (cip) against multiple manifests, activating credentials
# before each invocation.

usage()
{
    echo >&2 "usage: $0 <path/to/cip/binary> [<path/to/manifest.yaml>,<path/to/service-account.json>, ...]"
    echo >&2 "The 2nd argument onwards are '<manifest>,<service-account>,<service-account>...' strings."
    echo >&2 "If an execution does not require any service account, pass in '<manifest>,' (notice the trailing comma)."
    echo >&2
}

if (( $# < 2 )); then
    usage
    exit 1
fi

for opts in "$@"; do
    if ! [[ "$opts" =~ .*,.* ]]; then
        echo >&2 "invalid argument: $opts"
        usage
        exit 1
    fi
done

cip="$1"
shift

for opts in "$@"; do
    manifest=$(echo "$opts" | cut -d, -f1)
    service_accounts=()
    service_account_keyfiles=()
    while IFS= read -r keyfile; do
        if [[ -n "$keyfile" ]]; then
            service_account_keyfiles+=( "$keyfile" )
        fi
    done < <( echo "$opts" | cut -d, -f2- | tr , '\n' )

    # Only activate/deactivate service account files if they were passed in.
    if (( ${#service_account_keyfiles[@]} )); then
        for keyfile in "${service_account_keyfiles[@]}"; do
            # Authenticate as the service account. This allows the promoter to
            # later call gcloud with the flag `--account=...`. We can allow the
            # service account creds file to be empty, for testing cip locally (for
            # the case where the service account creds are already activated).
            gcloud auth activate-service-account --key-file="${keyfile}"
            service_accounts+=("$(gcloud config get-value account)")
        done

        # Run the promoter against the manifest.
        "${cip}" -verbosity=3 -manifest="${manifest}" ${CIP_OPTS:+$CIP_OPTS}

        # As a safety measure, deactivate all service accounts which were
        # activated with --key-file.
        gcloud auth revoke "${service_accounts[@]}"
    else
        "${cip}" -verbosity=3 -manifest="${manifest}" -no-service-account ${CIP_OPTS:+$CIP_OPTS}
    fi
done
