# This is not really the source of truth for the build system; refer to
# cloudbuild.yaml for that.

BAZEL_BUILD_OPTS:=--workspace_status_command=${PWD}/workspace_status.sh

all: test build
build:
	bazel build $(BAZEL_BUILD_OPTS) //:cip
image:
	bazel build $(BAZEL_BUILD_OPTS) //:cip-docker-loadable.tar
image-load: image
	docker load -i bazel-bin/cip-docker-loadable.tar
test:
	./lint.sh
	bazel test --test_output=all //lib/...

.PHONY: build docker-image test
