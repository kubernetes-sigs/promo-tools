#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace
shopt -s dotglob

# Remove the git archive that GCB auto-injects to /workspace, because it's
# not a real Git repository (it's just a tarball). This is required because
# we want to make use of git commands such as git-describe later on.
rm -rf /workspace/*
git \
  clone https://sigs.k8s.io/${_REPO} \
  --depth 1 \
  --branch $BRANCH_NAME \
  ${_GOPATH}/src/sigs.k8s.io/${_REPO}
# Get golangci-lint; install into /workspace/bin/golangci-lint.
pushd go
curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh \
      | sh -s ${_GOLANGCI_LINT_VERSION}