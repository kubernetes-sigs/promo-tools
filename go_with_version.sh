#!/usr/bin/env bash

# Copyright 2021 The Kubernetes Authors.
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

# About: Stamp internal/version with logging information before either
# building or running the provided go-code.
#
# Usage:
#   ./go_with_version.sh (build | run) path/to/code.go [args...]

set -o errexit
set -o nounset
set -o pipefail

printUsage() {
    >&2 echo "Usage: $0 (build | install | run ) path/to/code.go [args...]"
}

if [[ $# -lt 2 ]]; then
    >&2 echo "ERROR: Invalid number of arguments!"
    printUsage
    exit 1
elif [ "$1" != build ] && [ "$1" != install ] && [ "$1" != run ]; then
    >&2 echo "ERROR: First argument was not 'build', 'install', or 'run'!"
    printUsage
    exit 1
fi

tool="$2"
git_tree_state=dirty
pkg=sigs.k8s.io/promo-tools/internal/version

if git_status=$(git status --porcelain --untracked=no 2>/dev/null) && [[ -z "${git_status}" ]]; then
git_tree_state=clean
fi

go "$1" -v -ldflags "-s -w \
    -X $pkg.buildDate=$(date -u +'%Y-%m-%dT%H:%M:%SZ') \
    -X $pkg.gitCommit=$(git rev-parse HEAD 2>/dev/null || echo unknown) \
    -X $pkg.gitTreeState=$git_tree_state \
    -X $pkg.gitVersion=$(git describe --tags --abbrev=0 || echo unknown)" \
    "${@:2}"

echo "Finished running $tool"
