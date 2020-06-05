REPO_ROOT:=$(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

all: test
build:
	bazel build //:cip \
		//test-e2e/cip:e2e \
		//test-e2e/cip-auditor:cip-auditor-e2e \
		//cmd/promobot-files:promobot-files
image:
	bazel build //:cip-docker-loadable.tar
image-load: image
	docker load -i bazel-bin/cip-docker-loadable.tar
image-push: image
	bazel run :push-cip
image-load-cip-auditor-e2e:
	bazel build //test-e2e/cip-auditor:cip-docker-loadable-auditor-test.tar
	docker load -i bazel-bin/test-e2e/cip-auditor/cip-docker-loadable-auditor-test.tar
image-push-cip-auditor-e2e:
	bazel run //test-e2e/cip-auditor:push-cip-auditor-test
lint:
	GO111MODULE=on golangci-lint run
lint-ci: download
	make lint
test: build
	bazel test --test_output=all //...
# test-mac make target is a workaround for the following
# issue: https://github.com/bazelbuild/rules_go/issues/2013
test-mac:
	bazel test --test_output=all \
		//pkg/api/files:go_default_test \
		//lib/audit:go_default_test \
		//lib/dockerregistry:go_default_test \
		//pkg/cmd:go_default_test
test-ci: download
	make build
	make test
test-e2e-cip:
	bazel run //test-e2e/cip:e2e -- -tests=$(REPO_ROOT)/test-e2e/cip/tests.yaml -repo-root=$(REPO_ROOT) -key-file=$(CIP_E2E_KEY_FILE)
test-e2e-cip-auditor:
	bazel run //test-e2e/cip-auditor:cip-auditor-e2e -- -tests=$(REPO_ROOT)/test-e2e/cip-auditor/tests.yaml -repo-root=$(REPO_ROOT) -key-file=$(CIP_E2E_KEY_FILE)
download:
	GO111MODULE=on go mod download
update:
	# Update go modules (source of truth!).
	GO111MODULE=on go mod verify
	GO111MODULE=on go mod tidy
	# Update bazel rules to use these new dependencies.
	bazel run //:gazelle -- update-repos -prune -from_file=go.mod
	bazel run //:gazelle
.PHONY: build download image image-load image-push lint test test-e2e-cip test-e2e-cip-auditor update
