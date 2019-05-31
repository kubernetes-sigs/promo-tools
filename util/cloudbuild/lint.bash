#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

# Lint with golangci-lint. Because megacheck/typecheck linters require the
# `~/go/src/gopkg.in/yaml.v2` (external dependency) to be populated in the
# current GOPATH, we have to bring those in.
go get -u github.com/golang/dep/cmd/dep
export PATH=${_GOPATH}/bin:$$PATH
dep ensure
./lint.sh