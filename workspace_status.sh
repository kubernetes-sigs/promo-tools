#!/bin/bash

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

git_branch="$(git rev-parse --abbrev-ref HEAD)"
git_desc="$(git describe --always)"

p_ STABLE_IMG_REGISTRY gcr.io
p_ STABLE_IMG_REPOSITORY cip-demo-staging
p_ STABLE_IMG_NAME cip
p_ STABLE_IMG_TAG "${git_branch}-${git_desc}"
p_ STABLE_GIT_BRANCH "${git_branch}"
p_ STABLE_GIT_DESC "${git_desc}"
