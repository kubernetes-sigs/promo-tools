REPO_ROOT:=$(shell git rev-parse --show-toplevel)
BAZEL_BUILD_OPTS:=--workspace_status_command=$(REPO_ROOT)/workspace_status.sh --host_force_python=PY2

all: test build
build:
	bazel build $(BAZEL_BUILD_OPTS) //:cip
	bazel build $(BAZEL_BUILD_OPTS) //test-e2e:e2e
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
update:
	# Update go modules (source of truth!).
	GO111MODULE=on go mod verify
	GO111MODULE=on go mod tidy
	# Update bazel rules to use these new dependencies.
	bazel run $(BAZEL_BUILD_OPTS) //:gazelle -- update-repos -from_file=go.mod
	bazel run //:gazelle
.PHONY: build image image-load image-push lint test update
