all: test build
build:
	bazel build //:cip
image:
	bazel build //:cip-docker-loadable.tar
image-load: image
	docker load -i bazel-bin/cip-docker-loadable.tar
test:
	./lint.sh
	bazel test --test_output=all //lib/...

.PHONY: build docker-image test
