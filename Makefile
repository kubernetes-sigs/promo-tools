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

# If you update this file, please follow
# https://suva.sh/posts/well-documented-makefiles

REPO_ROOT:=$(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

all: test

GIT_VERSION=$(shell git describe --tags --always --dirty)
GIT_HASH ?= $(shell git rev-parse HEAD)
DATE_FMT = +%Y-%m-%dT%H:%M:%SZ
SOURCE_DATE_EPOCH ?= $(shell git log -1 --pretty=%ct)
ifdef SOURCE_DATE_EPOCH
    BUILD_DATE ?= $(shell date -u -d "@$(SOURCE_DATE_EPOCH)" "$(DATE_FMT)" 2>/dev/null || date -u -r "$(SOURCE_DATE_EPOCH)" "$(DATE_FMT)" 2>/dev/null || date -u "$(DATE_FMT)")
else
    BUILD_DATE ?= $(shell date "$(DATE_FMT)")
endif
GIT_TREESTATE = "clean"
DIFF = $(shell git diff --quiet >/dev/null 2>&1; if [ $$? -eq 1 ]; then echo "1"; fi)
ifeq ($(DIFF), 1)
    GIT_TREESTATE = "dirty"
endif

PKG=sigs.k8s.io/release-utils/version
LDFLAGS='"-X $(PKG).gitVersion=$(GIT_VERSION) -X $(PKG).gitCommit=$(GIT_HASH) -X $(PKG).gitTreeState=$(GIT_TREESTATE) -X $(PKG).buildDate=$(BUILD_DATE)"'
export CGO_ENABLED=1

.PHONY: kpromo
kpromo:
	go build \
		-trimpath \
		-ldflags '-s -w -buildid= -linkmode=external -extldflags=-static $(LDFLAGS)' \
		-tags osusergo \
		-o ./bin/kpromo \
		./cmd/kpromo

.PHONY: cip-mm
cip-mm: kpromo

##@ Build
.PHONY: build
build: kpromo ## Build go tools within the repository
	${REPO_ROOT}/go_with_version.sh build -o ./bin/cip-auditor-e2e ${REPO_ROOT}/test-e2e/cip-auditor/cip-auditor-e2e.go
	${REPO_ROOT}/go_with_version.sh build -o ./bin/cip-e2e ${REPO_ROOT}/test-e2e/cip/e2e.go

.PHONY: install
install: build ## Install
	${REPO_ROOT}/go_with_version.sh install ${REPO_ROOT}/cmd/kpromo

##@ Images

.PHONY: image-build ## Build auditor and cip images
image-build:
	./hack/cip-image.sh build

.PHONY: image-build-cip-auditor-e2e
image-build-cip-auditor-e2e: ## Build auditor e2e images
	./hack/cip-image.sh build --audit

.PHONY: image-push
image-push: image-build ## Build and push auditor and cip images
	./hack/cip-image.sh push

.PHONY: image-push-cip-auditor-e2e
image-push-cip-auditor-e2e: image-build-cip-auditor-e2e ## Build and push auditor e2e images
	./hack/cip-image.sh push --audit

##@ Lints

.PHONY: lint
lint:
	GO111MODULE=on golangci-lint run \
		-v \
		--timeout=5m

.PHONY: lint-ci
lint-ci: download
	make lint

##@ Tests

.PHONY: test
test: test-go-unit ## Runs unit tests

.PHONY: test-go-unit
test-go-unit: ## Runs Golang unit tests
	${REPO_ROOT}/hack/test-go.sh

.PHONY: test-ci
test-ci: download
	make test

.PHONY: test-e2e-cip
test-e2e-cip:
	${REPO_ROOT}/go_with_version.sh run ${REPO_ROOT}/test-e2e/cip/e2e.go \
		-tests=${REPO_ROOT}/test-e2e/cip/tests.yaml \
		-repo-root=${REPO_ROOT} \
		-key-file=${CIP_E2E_KEY_FILE}

.PHONY: test-e2e-cip-auditor
test-e2e-cip-auditor:
	${REPO_ROOT}/go_with_version.sh run ${REPO_ROOT}/test-e2e/cip-auditor/cip-auditor-e2e.go \
		-tests=${REPO_ROOT}/test-e2e/cip-auditor/tests.yaml \
		-repo-root=${REPO_ROOT} \
		-key-file=${CIP_E2E_KEY_FILE}

##@ Dependencies

.PHONY: download update

download: ## Download go modules
	GO111MODULE=on go mod download

update: ## Update go modules (source of truth!).
	GO111MODULE=on go mod verify
	GO111MODULE=on go mod tidy

update-mocks: ## Update all generated mocks
	go generate ./...
	for f in $(shell find . -name fake_*.go); do \
		cp hack/boilerplate/boilerplate.generatego.txt tmp ;\
		cat $$f >> tmp ;\
		mv tmp $$f ;\
	done

##@ Verify

.PHONY: verify verify-boilerplate verify-build verify-dependencies verify-golangci-lint verify-go-mod

verify: verify-boilerplate verify-dependencies verify-golangci-lint verify-go-mod verify-mocks verify-build ## Runs verification scripts to ensure correct execution

verify-boilerplate: ## Runs the file header check
	./hack/verify-boilerplate.sh

verify-build: ## Ensures repo CLI tools can be built
	./hack/verify-build.sh

verify-dependencies: ## Runs zeitgeist to verify dependency versions
	./hack/verify-dependencies.sh

verify-go-mod: ## Runs the go module linter
	./hack/verify-go-mod.sh

verify-golangci-lint: ## Runs all golang linters
	./hack/verify-golangci-lint.sh

verify-archives: ### Check golden image archives
	./hack/verify-archives.sh $(REPO_ROOT)

verify-mocks: ## Verify that mocks do not require updates
	./hack/verify-mocks.sh

##@ Helpers

.PHONY: help

help:  ## Display this help
	@awk \
		-v "col=${COLOR}" -v "nocol=${NOCOLOR}" \
		' \
			BEGIN { \
				FS = ":.*##" ; \
				printf "\nUsage:\n  make %s<target>%s\n", col, nocol \
			} \
			/^[a-zA-Z_-]+:.*?##/ { \
				printf "  %s%-15s%s %s\n", col, $$1, nocol, $$2 \
			} \
			/^##@/ { \
				printf "\n%s%s%s\n", col, substr($$0, 5), nocol \
			} \
		' $(MAKEFILE_LIST)
