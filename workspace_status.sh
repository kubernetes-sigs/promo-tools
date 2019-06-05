#!/usr/bin/env bash

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

git_commit="$(git rev-parse HEAD)"
git_desc="$(git describe --always --dirty --long)"
timestamp_utc_rfc3339=$(date --utc --rfc-3339=seconds)
timestamp_utc_date_dashes="${timestamp_utc_rfc3339% *}"
timestamp_utc_date_no_dashes="${timestamp_utc_date_dashes//-/}"
image_tag="$git_desc"

# Image Promoter Image
p_ STABLE_IMG_REGISTRY gcr.io
p_ STABLE_IMG_REPOSITORY cip-demo-staging
p_ STABLE_IMG_NAME cip

# Cloud Build Images 
p_ CLOUD_BUILD_IMG_REGISTRY gcr.io
p_ CLOUD_BUILD_IMG_REPOSITORY cloud-builders
p_ CLOUD_BUILD_GIT_IMG_NAME git
p_ CLOUD_BUILD_GIT_IMG_TAG latest
p_ CLOUD_BUILD_GO_IMG_NAME go
p_ CLOUD_BUILD_GO_IMG_TAG debian
p_ CLOUD_BUILD_BAZEL_IMG_NAME bazel
p_ CLOUD_BUILD_BAZEL_IMG_TAG latest

# Git Hashes
p_ STABLE_GIT_COMMIT "${git_commit}"
p_ STABLE_GIT_DESC "${git_desc}"
p_ TIMESTAMP_UTC_RFC3339 "${timestamp_utc_rfc3339}"
p_ IMG_TAG "${timestamp_utc_date_no_dashes}-${image_tag}"
