#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

bazel test --test_output=all //lib/...