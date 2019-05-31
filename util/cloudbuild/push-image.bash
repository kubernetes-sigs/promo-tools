#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

bazel run --workspace_status_command=$${PWD}/workspace_status.sh :push-cip-latest
bazel run --workspace_status_command=$${PWD}/workspace_status.sh :push-cip-tagged