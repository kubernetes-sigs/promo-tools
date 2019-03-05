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

# See
# https://stackoverflow.com/questions/59895/getting-the-source-directory-of-a-bash-script-from-within#comment49538168_246128
# and https://gist.github.com/tvlooy/cbfbdb111a4ebad8b93e.
SCRIPT_ROOT="$(dirname "$(readlink -f "$0")")"

echo "=== Linting ==="
cd "${SCRIPT_ROOT}"
if golangci-lint run; then
    echo "PASS"
else
    err=$?
    echo >&2 "FAIL"
    exit "${err}"
fi
