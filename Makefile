BAZEL_BUILD_OPTS:=--workspace_status_command=${PWD}/workspace_status.sh

all: test build
build:
	bazel build $(BAZEL_BUILD_OPTS) //:cip
image:
	bazel build $(BAZEL_BUILD_OPTS) //:cip-docker-loadable.tar
image-load: image
	docker load -i bazel-bin/cip-docker-loadable.tar
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

	# Fixup vendor/ folder to make golangci-lint happy.
	rm -rf vendor
	GO111MODULE=on go mod vendor

.PHONY: build image image-load lint test update
