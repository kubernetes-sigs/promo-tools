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

VERSION=v2.4.0
URL_BASE=https://raw.githubusercontent.com/golangci/golangci-lint
URL=${URL_BASE}/${VERSION}/install.sh
# If you update the version above you might need to update the checksum
# if it does not match. We say might because in the past the install script
# has been unchanged even if there is a new verion of golangci-lint.
# To obtain the checksum, download the install script and run the following
# command:
# > sha256sum <path-to-install-script>
INSTALL_CHECKSUM=8e7a5b7d112128d7a700755a9064b20bdc8d2834c4f6ad7614af2274aa51a20b

if [[ ! -f .golangci.yml ]]; then
    echo 'ERROR: missing .golangci.yml in repo root' >&2
    exit 1
fi

if ! command -v golangci-lint; then
    INSTALL_SCRIPT=$(mktemp -d)/install.sh
    curl -sfL "${URL}" >"${INSTALL_SCRIPT}"
    # Cross-platform sha256sum/shasum check
    system=$(uname)
    if [[ "${system}" == "Darwin" ]]; then
        CHECK_CMD="echo \"${INSTALL_CHECKSUM}  ${INSTALL_SCRIPT}\" | shasum -a 256 --check --status"
    else
        CHECK_CMD="echo \"${INSTALL_CHECKSUM}  ${INSTALL_SCRIPT}\" | sha256sum --check --status"
    fi
    if eval "${CHECK_CMD}"; then
        chmod 755 "${INSTALL_SCRIPT}"
        ${INSTALL_SCRIPT} -b /tmp "${VERSION}"
        export PATH=${PATH}:/tmp
        pwd
    else
        echo 'ERROR: install script sha256 checksum invalid' >&2
        exit 1
    fi
fi

golangci-lint version
golangci-lint linters
golangci-lint run "$@"
