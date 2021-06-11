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

if [[ $* != *-repo-root* ]]
then
    echo "Error: -repo-root flag not specified" 1>&2
    exit 1
fi

repoRoot=$(cut -d "=" -f2 <<< "$*")
images=("bar/1.0" "foo/1.0-linux_amd64" "foo/1.0-linux_s390x" "foo/NOTAG-0")
# setup mock directory structure
mkdir mock-golden
mkdir mock-golden/foo
mkdir mock-golden/bar

goldenArchives="${repoRoot}/test-e2e/cip/golden-archives/"
goldenContents="${repoRoot}/test-e2e/cip/golden"

for path in "${images[@]}"
do
    img=$(docker load -i "${goldenArchives}${path}.tar")
    img=$(cut -d " " -f3 <<< "$img")
    id=$(docker create "$img" /dev/null)
    # specify destination for copy
    dest="./mock-golden/"$(cut -d "/" -f1 <<< "$path")
    # copy data file inside container
    file=$(cut -d "/" -f2 <<< "$path")
    docker cp "$id":/"$file" "$dest"
done

# compare contents
diff -r ./mock-golden "$goldenContents"

# cleanup
rm -rf mock-golden