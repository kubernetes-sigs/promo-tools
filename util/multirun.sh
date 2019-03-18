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
cat <<EOF >&2
usage: $0 <path/to/cip/binary> [<MANIFEST>,<KEYFILE>[,<KEYFILE>,...] ...]

MANIFEST: path/to/manifest.yaml
KEYFILE: path/to/service-account.json

The 2nd argument onwards are

    '<manifest>,<service-account>,<service-account>...'

strings.

If an execution does not require any service account, pass in '<manifest>,'
(notice the trailing comma).

If you only want to run against manifests that changed in a Git commitish,
specify the CIP_GIT_REV and CIP_GIT_DIR environment variables. E.g.,
CIP_GIT_REV=HEAD
CIP_GIT_DIR=/path/to/git/repo

EOF
}

# Filter the given list of files by checking if it is a Manifest file. Files
# must be absolute paths.
collect_manifests()
{
    local cip="$1"
    shift 1

    for f in "$@"; do
        if "$cip" -parse-only -manifest="$f"; then
            echo "$f"
        fi
    done
}

if (( $# < 2 )); then
    usage
    exit 1
fi

cip="$1"
shift
args=("$@")

for arg in "${args[@]}"; do
    if ! [[ "$arg" =~ .*,.* ]]; then
        echo >&2 "invalid argument: $arg"
        usage
        exit 1
    fi
done

# If we only want to run against changed manifest files, then filter out those
# files that were not changed since the last commit.
if [[ "${CIP_GIT_REV:-}" ]]; then
    if [[ ! -d "${CIP_GIT_DIR:-}" ]]; then
        echo >&2 "CIP_GIT_DIR not set (must be set if CIP_GIT_REV is set)"
        usage
        exit 1
    fi
    pushd "${CIP_GIT_DIR}"
    changed_files=()
    while IFS= read -r f; do
        changed_files+=( "$f" )
    done < <( git diff-tree --no-commit-id --name-only -r "${CIP_GIT_REV}" )
    popd

    changed_manifests=$(collect_manifests "${cip}" "${changed_files[@]}")

    args_filtered=()
    for arg in "${args[@]}"; do
        manifest=$(echo "$arg" | cut -d, -f1)
        manifest_comparable=$(basename "${manifest}")
        if echo "${changed_manifests[@]}" | grep "${manifest_comparable}"; then
          args_filtered+=("${arg}")
        fi
    done

    if (( "${#args_filtered[@]}" == 0 )); then
        echo "No manifests were changed in ${CIP_GIT_REV}."
        exit 0
    fi

    args=("${args_filtered[@]}")
fi

for arg in "${args[@]}"; do
    manifest=$(echo "$arg" | cut -d, -f1)
    service_accounts=()
    service_account_keyfiles=()
    while IFS= read -r keyfile; do
        if [[ -n "$keyfile" ]]; then
            service_account_keyfiles+=( "$keyfile" )
        fi
    done < <( echo "$arg" | cut -d, -f2- | tr , '\n' )

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
