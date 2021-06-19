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

set -o errexit
set -o nounset
set -o pipefail

#`p_` takes two arguments to define a bazel workspace status variable:
#
#  * the name of the variable
#  * a default value
#
# If an environment variable with the corresponding name is set, its value is
# used. Otherwise, the provided default value is used.
p_() {
    if (( $# == 2 )); then
        echo "$1" "${!1:-$2}"
    else
        return 1
    fi
}

# If a PROW_GIT_TAG variable is provided by Prow's staging image building
# mechanism, use it instead of asking `git` directly, because the repo we are in
# might not actually be a Git repo.
if [[ -n "${PROW_GIT_TAG:-}" ]]; then
    # Ensure that the PROW_GIT_TAG fits a known pattern of
    # vYYYYMMDD-tag-n-ghash. According to
    # https://github.com/kubernetes/test-infra/blob/f7e21a3c18f4f4bbc7ee170675ed53e4544a0632/config/jobs/image-pushing/README.md#custom-substitutions,
    # the PROW_GIT_TAG will be one of vYYYYMMDD-hash, vYYYYMMDD-tag, or
    # vYYYYMMDD-tag-n-ghash.
    if [[ "${PROW_GIT_TAG}" =~ v[0-9]{8}-[^-]+-[0-9]+-g[0-9a-f]{7} ]]; then
        # In this case we have a "-n-ghash" suffix, which we can use to set the
        # git_commit and git_desc information.

        # Extract the last 7 characters.
        git_commit="${PROW_GIT_TAG:(-7)}"
        # Skip built-in date in PROW_GIT_TAG.
        git_desc="${PROW_GIT_TAG:(10)}"
    # If the -n-ghash bit is missing, then it must look like vYYYYMMDD-hash or
    # vYYYYMMDD-tag. We can't easily tell if something is a hash or a tag, so we
    # just use it as-is.
    else
        # Extract the hash or tag (we can't tell easily).
        git_commit="${PROW_GIT_TAG:(10)}"
        # We just reuse git_commit (could be a hash or tag) here because it's
        # the best we can do.
        git_desc="${git_commit}"
    fi

    timestamp_utc_rfc3339=$(date -u +"%Y-%m-%d %H:%M:%S%z")
    timestamp_utc_date_dashes="${timestamp_utc_rfc3339% *}"
    timestamp_utc_date_no_dashes="${timestamp_utc_date_dashes//-/}"
else
    git_commit="$(git rev-parse HEAD)"
    git_desc_raw="$(git describe --always --dirty --long)"
    git_desc="${git_desc_raw//\//-}"
    timestamp_utc_rfc3339=$(date -u +"%Y-%m-%d %H:%M:%S%z")
    timestamp_utc_date_dashes="${timestamp_utc_rfc3339% *}"
    timestamp_utc_date_no_dashes="${timestamp_utc_date_dashes//-/}"
fi

p_ STABLE_TEST_AUDIT_PROD_IMG_REPOSITORY us.gcr.io/k8s-gcr-audit-test-prod
p_ STABLE_TEST_AUDIT_STAGING_IMG_REPOSITORY gcr.io/k8s-gcr-audit-test-prod
p_ STABLE_TEST_AUDIT_PROJECT_ID k8s-gcr-audit-test-prod
p_ STABLE_TEST_AUDIT_PROJECT_NUMBER 375340694213
p_ STABLE_TEST_AUDIT_INVOKER_SERVICE_ACCOUNT k8s-infra-gcr-promoter@k8s-gcr-audit-test-prod.iam.gserviceaccount.com
p_ STABLE_TEST_STAGING_IMG_REPOSITORY gcr.io/k8s-staging-cip-test
p_ STABLE_IMG_REGISTRY gcr.io
p_ STABLE_IMG_REPOSITORY k8s-staging-artifact-promoter
p_ STABLE_IMG_NAME cip
p_ STABLE_GIT_COMMIT "${git_commit}"
p_ STABLE_GIT_DESC "${git_desc}"
p_ TIMESTAMP_UTC_RFC3339 "${timestamp_utc_rfc3339}"
p_ IMG_TAG "${timestamp_utc_date_no_dashes}-${git_desc}"
