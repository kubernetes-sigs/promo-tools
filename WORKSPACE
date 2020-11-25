load("@bazel_tools//tools/build_defs/repo:git.bzl", "git_repository")
load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

http_archive(
    name = "rules_pkg",
    sha256 = "aeca78988341a2ee1ba097641056d168320ecc51372ef7ff8e64b139516a4937",
    urls = [
        "https://github.com/bazelbuild/rules_pkg/releases/download/0.2.6-1/rules_pkg-0.2.6.tar.gz",
        "https://mirror.bazel.build/github.com/bazelbuild/rules_pkg/releases/download/0.2.6/rules_pkg-0.2.6.tar.gz",
    ],
)

load("@rules_pkg//:deps.bzl", "rules_pkg_dependencies")

rules_pkg_dependencies()

# You *must* import the Go rules before setting up the go_image rules.
http_archive(
    name = "io_bazel_rules_go",
    sha256 = "207fad3e6689135c5d8713e5a17ba9d1290238f47b9ba545b63d9303406209c6",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/rules_go/releases/download/v0.24.7/rules_go-v0.24.7.tar.gz",
        "https://github.com/bazelbuild/rules_go/releases/download/v0.24.7/rules_go-v0.24.7.tar.gz",
    ],
)

# Invoke go_rules_dependencies depending on host platform.
# See https://github.com/tweag/rules_purescript/commit/730d38ddb9db63f1e674715b54f813a9a9765b36
local_repository(
    name = "go_sdk_repo",
    path = "tools/go_sdk",
)

load("@go_sdk_repo//:sdk.bzl", "gen_imports")

gen_imports(name = "go_sdk_imports")

load("@go_sdk_imports//:imports.bzl", "load_go_sdk")

load_go_sdk()

http_archive(
    name = "bazel_gazelle",
    sha256 = "b85f48fa105c4403326e9525ad2b2cc437babaa6e15a3fc0b1dbab0ab064bc7c",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/bazel-gazelle/releases/download/v0.22.2/bazel-gazelle-v0.22.2.tar.gz",
        "https://github.com/bazelbuild/bazel-gazelle/releases/download/v0.22.2/bazel-gazelle-v0.22.2.tar.gz",
    ],
)

load("@bazel_gazelle//:deps.bzl", "gazelle_dependencies")
load("//:repos.bzl", "go_repositories")

# gazelle:repository_macro repos.bzl%go_repositories
go_repositories()

gazelle_dependencies()

http_archive(
    name = "io_bazel_rules_docker",
    sha256 = "4521794f0fba2e20f3bf15846ab5e01d5332e587e9ce81629c7f96c793bb7036",
    strip_prefix = "rules_docker-0.14.4",
    urls = ["https://github.com/bazelbuild/rules_docker/releases/download/v0.14.4/rules_docker-v0.14.4.tar.gz"],
)

load(
    "@io_bazel_rules_docker//repositories:repositories.bzl",
    container_repositories = "repositories",
)

container_repositories()

load("@io_bazel_rules_docker//repositories:deps.bzl", container_deps = "deps")

container_deps()

load("@io_bazel_rules_docker//repositories:pip_repositories.bzl", "pip_deps")

pip_deps()

load(
    "@io_bazel_rules_docker//go:image.bzl",
    _go_image_repos = "repositories",
)

_go_image_repos()

load("@io_bazel_rules_docker//container:container.bzl", "container_pull")

container_pull(
    name = "google-sdk-base",
    # Version 241.0.0
    digest = "sha256:3b77ee8bfa6a2513fb6343cfad0dd6fd6ddd67d0632908c3a5fb9b57dd68ec1b",
    registry = "index.docker.io",
    repository = "google/cloud-sdk",
)

# Maybe use cloud-builders/gcloud, for GCB. But for Prow just use the google-sdk
# one.
#container_pull(
#    name = "google-sdk-base",
#    registry = "gcr.io",
#    repository = "cloud-builders/gcloud",
#    # Version 232.0.0
#    digest = "sha256:6e6b1e2fd53cb94c4dc2af8381ef50bf4c7ac49bc5c728efda4ab15b41d0b510",
#)

# Rules for installing binaries into the local filesystem.
#
# Replace with http_archive() (official rules install instructions) after
# https://github.com/google/bazel_rules_install/pull/27 is merged.
git_repository(
    name = "com_github_google_rules_install",
    commit = "84406bab66bbb5061621d3b4b782c453e22c160a",
    remote = "https://github.com/listx/bazel_rules_install",
    shallow_since = "1588201339 -0700",
)

load("@com_github_google_rules_install//:deps.bzl", "install_rules_dependencies")

install_rules_dependencies()

load("@com_github_google_rules_install//:setup.bzl", "install_rules_setup")

install_rules_setup()

container_pull(
    name = "distroless-base",
    # Version 241.0.0
    digest = "sha256:edc3643ddf96d75032a55e240900b68b335186f1e5fea0a95af3b4cc96020b77",
    registry = "gcr.io",
    repository = "distroless/base",
)

git_repository(
    name = "com_google_protobuf",
    commit = "09745575a923640154bcf307fba8aedff47f240a",
    remote = "https://github.com/protocolbuffers/protobuf",
    shallow_since = "1558721209 -0700",
)

load("@com_google_protobuf//:protobuf_deps.bzl", "protobuf_deps")

protobuf_deps()
