#!/usr/bin/env bash

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
specify the CIP_GIT_DIR, CIP_GIT_REV_START, and CIP_GIT_REV_END environment
variables.

E.g.,
CIP_GIT_DIR=/path/to/git/repo
CIP_GIT_REV_START=master
CIP_GIT_REV_END=HEAD

Note that if these variables are defined, the manifest file paths must be
defined relative to CIP_GIT_DIR; this also means that this script must be
executed from CIP_GIT_DIR.

EOF
}

if (( $# < 2 )); then
    usage
    exit 1
fi

cip="$1"
shift

# If CIP_GIT_REV_{START,END} is defined, remove manifests that are not found as
# modified files in the commit range. That is, don't look at manifest files that
# were not modified from CIP_GIT_REV_START to CIP_GIT_REV_END.
args_final=("$@")
if [[ -d "${CIP_GIT_DIR:-}" ]]; then
    # We are running inside Prow, set up the CIP_GIT_REV_{START,END} vars
    # automatically.
    if [[ -n "${PROW_JOB_ID:-}" ]]; then
        CIP_GIT_REV_START="${PULL_BASE_SHA}"
        CIP_GIT_REV_END="${PULL_PULL_SHA}"
    fi
    if [[ -z "${CIP_GIT_REV_START:-}" ]]; then
        echo >&2 "CIP_GIT_REV_START not set (must be set if CIP_GIT_DIR is set)"
        usage
        exit 1
    fi
    if [[ -z "${CIP_GIT_REV_END:-}" ]]; then
        echo >&2 "CIP_GIT_REV_END not set (must be set if CIP_GIT_DIR is set)"
        usage
        exit 1
    fi

    cmd="git -C ${CIP_GIT_DIR} diff --name-only ${CIP_GIT_REV_START}..${CIP_GIT_REV_END}"
    echo checking changed files with "${cmd}"
    readarray -t changed_files < <(${cmd})

    # Filter out those manifests that were not modified. The net effect is that
    # if this script was invoked with:
    #
    #   ... manifest_a.yaml,keyfile_1 manifest_b.yaml,keyfile_2 manifest_c.yaml,keyfile_3
    #
    # but only manifest_a.yaml and manifest_c.yaml were changed from
    # CIP_GIT_REV_START to CIP_GIT_REV_END, the "manifest_b.yaml,keyfile_2"
    # argument would be removed, thus effectivly changing the invocation to:
    #
    #   ... manifest_a.yaml,keyfile_1 manifest_c.yaml,keyfile_3
    #
    # Note that this is not fully bullet-proof (it may be that some manifests
    # changed comment lines or whitespace --- resulting in no semantic change),
    # but that will change once the promoter understands deltas [1].
    #
    # [1]: https://github.com/kubernetes-sigs/k8s-container-image-promoter/issues/10
    args_filtered=()
    for arg; do
        manifest=$(echo "$arg" | cut -d, -f1)
        if printf '%s\n' "${changed_files[@]}" | grep "${manifest}"; then
          args_filtered+=("${arg}")
        fi
    done

    if (( "${#args_filtered[@]}" == 0 )); then
        echo "No manifests were changed in ${CIP_GIT_REV_START}..${CIP_GIT_REV_END}."
        exit 0
    fi

    args_final=("${args_filtered[@]}")
fi

echo "MULTIRUN: container image promoter version:"
"${cip}" -version
echo "MULTIRUN: gcloud version:"
gcloud version

for arg in "${args_final[@]}"; do
    echo
    echo "MULTIRUN: running against ${CIP_GIT_DIR:+$CIP_GIT_DIR/}${manifest}"

    service_accounts=()
    # Split a line into an array.
    # See https://stackoverflow.com/a/45201229/437583.
    readarray -td, service_account_keyfiles <<< "$arg,"
    # Trim empty element (due to trailing ',' we pass into the <<< operator).
    unset 'service_account_keyfiles[-1]'

    # Slurp manifest out of array.
    manifest="${service_account_keyfiles[0]}"
    # Trim manifest out of service_account_keyfiles.
    unset 'service_account_keyfiles[0]'

    # Only activate/deactivate service account files if they were passed in.
    if (( ${#service_account_keyfiles[@]} )); then
        for keyfile in "${service_account_keyfiles[@]}"; do
            # Authenticate as the service account. This allows the promoter to
            # later call gcloud with the flag `--account=...`. We can allow the
            # service account creds file to be empty, for testing cip locally (for
            # the case where the service account creds are already activated).
            echo "MULTIRUN: activating service account ${keyfile}"
            gcloud auth activate-service-account --key-file="${keyfile}"
            service_accounts+=("$(gcloud config get-value account)")
        done

        # Run the promoter against the manifest.
        "${cip}" -verbosity=3 -manifest="${manifest}" ${CIP_OPTS:+$CIP_OPTS}

        # As a safety measure, deactivate all service accounts which were
        # activated with --key-file.
        echo "MULTIRUN: revoking service account(s) ${service_accounts[*]}"
        gcloud auth revoke "${service_accounts[@]}"
    else
        "${cip}" -verbosity=3 -manifest="${CIP_GIT_DIR:+$CIP_GIT_DIR/}""${manifest}" -no-service-account ${CIP_OPTS:+$CIP_OPTS}
    fi
done
