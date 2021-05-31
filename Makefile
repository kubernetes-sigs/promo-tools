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

##@ Build
.PHONY: build
build: ## Bazel build
	bazel build //cmd/cip:cip \
		//test-e2e/cip:e2e \
		//test-e2e/cip-auditor:cip-auditor-e2e

.PHONY: install
install: ## Install
	bazel run //:install-cip -c opt -- $(shell go env GOPATH)/bin

##@ Images

.PHONY: image
image: ## Build image
	bazel build //:cip-docker-loadable.tar

.PHONY: image-load
image-load: image ## Build image and load it
	docker load -i bazel-bin/cip-docker-loadable.tar

.PHONY: image-push
image-push: image ## Build image and push
	bazel run :push-cip

.PHONY: image-load-cip-auditor-e2e
image-load-cip-auditor-e2e: ## Build and load image cip-auditor-e2e
	bazel build //test-e2e/cip-auditor:cip-docker-loadable-auditor-test.tar
	docker load -i bazel-bin/test-e2e/cip-auditor/cip-docker-loadable-auditor-test.tar

.PHONY: image-push-cip-auditor-e2e
image-push-cip-auditor-e2e: ## Push image cip-auditor-e2e
	bazel run //test-e2e/cip-auditor:push-cip-auditor-test

##@ Lints

.PHONY: lint
lint: ## Build image-load-cip-auditor-e2e
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
	bazel run //test-e2e/cip:e2e -- -tests=$(REPO_ROOT)/test-e2e/cip/tests.yaml -repo-root=$(REPO_ROOT) -key-file=$(CIP_E2E_KEY_FILE)

.PHONY: test-e2e-cip-auditor
test-e2e-cip-auditor:
	bazel run //test-e2e/cip-auditor:cip-auditor-e2e -- -tests=$(REPO_ROOT)/test-e2e/cip-auditor/tests.yaml -repo-root=$(REPO_ROOT) -key-file=$(CIP_E2E_KEY_FILE)

##@ Dependencies

.PHONY: download update

download: ## Download go modules
	GO111MODULE=on go mod download

update: ## Update go modules (source of truth!).
	GO111MODULE=on go mod verify
	GO111MODULE=on go mod tidy
	# Update bazel rules to use these new dependencies.
	bazel run //:gazelle -- update-repos \
		--from_file=go.mod --to_macro=repos.bzl%go_repositories \
		--build_file_generation=on --build_file_proto_mode=disable_global \
		--prune
	bazel run //:gazelle -- fix

##@ Verify

.PHONY: verify verify-boilerplate verify-dependencies verify-golangci-lint verify-go-mod

verify: verify-boilerplate verify-dependencies verify-golangci-lint verify-go-mod ## Runs verification scripts to ensure correct execution

verify-boilerplate: ## Runs the file header check
	./hack/verify-boilerplate.sh

verify-dependencies: ## Runs zeitgeist to verify dependency versions
	./hack/verify-dependencies.sh

verify-go-mod: ## Runs the go module linter
	./hack/verify-go-mod.sh

verify-golangci-lint: ## Runs all golang linters
	./hack/verify-golangci-lint.sh

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
