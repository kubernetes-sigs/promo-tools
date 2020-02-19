REPO_ROOT:=$(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
BAZEL_BUILD_OPTS:=--workspace_status_command=$(REPO_ROOT)/workspace_status.sh \
	--host_force_python=PY2 \
	--stamp

all: test build
build:
	bazel build $(BAZEL_BUILD_OPTS) //:cip \
		//test-e2e/cip:e2e \
		//test-e2e/cip-auditor:cip-auditor-e2e \
		//cmd/promobot-files:promobot-files
image:
	bazel build $(BAZEL_BUILD_OPTS) //:cip-docker-loadable.tar
image-load: image
	docker load -i bazel-bin/cip-docker-loadable.tar
image-push: image
	bazel run $(BAZEL_BUILD_OPTS) :push-cip
image-load-cip-auditor-e2e:
	bazel build $(BAZEL_BUILD_OPTS) //test-e2e/cip-auditor:cip-docker-loadable-auditor-test.tar
	docker load -i bazel-bin/test-e2e/cip-auditor/cip-docker-loadable-auditor-test.tar
image-push-cip-auditor-e2e:
	bazel run $(BAZEL_BUILD_OPTS) //test-e2e/cip-auditor:push-cip-auditor-test
lint:
	GO111MODULE=on golangci-lint run
lint-ci: download
	make lint
test:
	bazel test --test_output=all //...
test-ci: download
	make build
	make test
test-e2e-cip:
	bazel run $(BAZEL_BUILD_OPTS) //test-e2e/cip:e2e -- -tests=$(REPO_ROOT)/test-e2e/cip/tests.yaml -repo-root=$(REPO_ROOT) -key-file=$(CIP_E2E_KEY_FILE)
test-e2e-cip-auditor:
	bazel run $(BAZEL_BUILD_OPTS) //test-e2e/cip-auditor:cip-auditor-e2e -- -tests=$(REPO_ROOT)/test-e2e/cip-auditor/tests.yaml -repo-root=$(REPO_ROOT) -key-file=$(CIP_E2E_KEY_FILE)
download:
	GO111MODULE=on go mod download
update:
	# Update go modules (source of truth!).
	GO111MODULE=on go mod verify
	GO111MODULE=on go mod tidy
	# Update bazel rules to use these new dependencies.
	bazel run $(BAZEL_BUILD_OPTS) //:gazelle -- update-repos -prune -from_file=go.mod
	bazel run //:gazelle
.PHONY: build download image image-load image-push lint test test-e2e-cip test-e2e-cip-auditor update
