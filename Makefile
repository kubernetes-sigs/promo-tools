REPO_ROOT:=$(shell git rev-parse --show-toplevel)
BAZEL_BUILD_OPTS:=--workspace_status_command=$(REPO_ROOT)/workspace_status.sh \
	--host_force_python=PY2 \
	--stamp

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
test-e2e:
	bazel run $(BAZEL_BUILD_OPTS) //test-e2e:e2e -- -tests=$(REPO_ROOT)/test-e2e/tests.yaml -repo-root=$(REPO_ROOT) -key-file=$(CIP_E2E_KEY_FILE)
update:
	# Update go modules (source of truth!).
	GO111MODULE=on go mod verify
	GO111MODULE=on go mod tidy
	# Update bazel rules to use these new dependencies.
	bazel run $(BAZEL_BUILD_OPTS) //:gazelle -- update-repos -prune -from_file=go.mod
	bazel run //:gazelle
.PHONY: build image image-load image-push lint test test-e2e update
