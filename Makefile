REPO_ROOT:=$(shell git rev-parse --show-toplevel)
BAZEL_BUILD_OPTS:=--workspace_status_command=$(REPO_ROOT)/workspace_status.sh --host_force_python=PY2

all: test build
build:
	bazel build $(BAZEL_BUILD_OPTS) //:cip
	bazel build $(BAZEL_BUILD_OPTS) //test-e2e:e2e
	bazel build $(BAZEL_BUILD_OPTS) //cmd/promobot-files:promobot-files
image:
	bazel build $(BAZEL_BUILD_OPTS) //:cip-docker-loadable.tar
image-load: image
	docker load -i bazel-bin/cip-docker-loadable.tar
image-push: image
	bazel run $(BAZEL_BUILD_OPTS) :push-cip
lint:
	@./lint.sh
test:
	bazel test --test_output=all //lib/...
image-e2e-env:
	bazel build $(BAZEL_BUILD_OPTS) //:e2e-env-docker-loadable.tar
image-load-e2e-env: image-e2e-env
	docker load -i bazel-bin/e2e-env-docker-loadable.tar
image-push-e2e-env: image-load-e2e-env
	bazel run $(BAZEL_BUILD_OPTS) :push-e2e-env
test-e2e:
	make && ./bazel-bin/test-e2e/linux_amd64_stripped/e2e -tests=$(PWD)/test-e2e/tests.yaml -repo-root $(PWD) -key-file $(CIP_E2E_KEY_FILE)
update:
	# Update go modules (source of truth!).
	GO111MODULE=on go mod verify
	GO111MODULE=on go mod tidy
	# Update bazel rules to use these new dependencies.
	bazel run $(BAZEL_BUILD_OPTS) //:gazelle -- update-repos -prune -from_file=go.mod
	bazel run //:gazelle
.PHONY: build image image-load image-push lint test test-e2e update
