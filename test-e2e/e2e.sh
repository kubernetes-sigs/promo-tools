#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT="$(dirname "$(readlink -f "$0")")"
REPO_ROOT="$(git rev-parse --show-toplevel)"
WORKSPACE_STATUS_CMD="${REPO_ROOT}/workspace_status.sh"

# Delete, populate, and also invoke multirun.sh against a number of
# manifest.yaml files.

get_bazel_option()
{
    local key
    local val

    if (( $# != 1 )); then
        echo >&2 "usage: get_bazel_option KEY"
        return 1
    fi

    key="$1"

    val=$($WORKSPACE_STATUS_CMD | grep "${key}" | cut -d' ' -f2-)
    if [[ -z $val ]]; then
        echo >&2 "key $key not found"
        return 1
    fi

    printf '%s\n' "${val}"
}

e2e_populate()
{
    local push_repo
    local bazel_opts

    pushd "${SCRIPT_ROOT}"/..
        bazel_opts=(
            "--host_force_python=PY2"
            "--workspace_status_command=${WORKSPACE_STATUS_CMD}")
        bazel build "${bazel_opts[@]}" //test-e2e:golden-images-loadable.tar
        # In order to create a manifest list, images must be pushed to a
        # repository first.
        bazel run "${bazel_opts[@]}" //test-e2e:push-golden
    popd

    push_repo=$(get_bazel_option STABLE_TEST_STAGING_IMG_REPOSITORY)
    echo "push_repo is $push_repo"

    docker manifest create "${push_repo}/golden:1.0" \
        "${push_repo}/golden:1.0-linux_amd64" \
        "${push_repo}/golden:1.0-linux_s390x"

    # Fixup the s390x image because it's set to amd64 by default (there is no
    # way to specify architecture from within bazel yet when creating images).
    docker manifest annotate --arch s390x "${push_repo}/golden:1.0" "${push_repo}/golden:1.0-linux_s390x"
    docker manifest inspect "${push_repo}/golden:1.0"
    # Finally, push the manifest list. It is just metadata around existing
    # images in a repository.
    docker manifest push --purge "${push_repo}/golden:1.0"
}

if (( $# != 1 )); then
    echo >&2 "usage: e2e.sh <MODE>"
    exit 1
fi

mode="$1"

shift 1

case $mode in
    populate) e2e_populate ;;
    *)
		echo >&2 "unknown mode \`${mode}'"
	;;
esac
