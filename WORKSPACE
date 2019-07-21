load("@bazel_tools//tools/build_defs/repo:git.bzl", "git_repository")
load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

git_repository(
    name = "bazel_skylib",
    remote = "https://github.com/bazelbuild/bazel-skylib.git",
    tag = "0.9.0"
)

# You *must* import the Go rules before setting up the go_image rules.
http_archive(
    name = "io_bazel_rules_go",
    urls = ["https://github.com/bazelbuild/rules_go/releases/download/0.18.6/rules_go-0.18.6.tar.gz"],
    sha256 = "f04d2373bcaf8aa09bccb08a98a57e721306c8f6043a2a0ee610fd6853dcde3d",
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
    urls = ["https://github.com/bazelbuild/bazel-gazelle/releases/download/0.17.0/bazel-gazelle-0.17.0.tar.gz"],
    sha256 = "3c681998538231a2d24d0c07ed5a7658cb72bfb5fd4bf9911157c0e9ac6a2687",
)

load("@bazel_gazelle//:deps.bzl", "gazelle_dependencies", "go_repository")

gazelle_dependencies()

http_archive(
    name = "io_bazel_rules_docker",
    sha256 = "aed1c249d4ec8f703edddf35cbe9dfaca0b5f5ea6e4cd9e83e99f3b0d1136c3d",
    strip_prefix = "rules_docker-0.7.0",
    urls = ["https://github.com/bazelbuild/rules_docker/archive/v0.7.0.tar.gz"],
)

load(
    "@io_bazel_rules_docker//go:image.bzl",
    _go_image_repos = "repositories",
)

_go_image_repos()

load("@io_bazel_rules_docker//container:container.bzl", "container_pull")

container_pull(
    name = "google-sdk-base",
    registry = "index.docker.io",
    repository = "google/cloud-sdk",
    # Version 241.0.0
    digest = "sha256:3b77ee8bfa6a2513fb6343cfad0dd6fd6ddd67d0632908c3a5fb9b57dd68ec1b",
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

container_pull(
    name = "distroless-base",
    registry = "gcr.io",
    repository = "distroless/base",
    # Version 241.0.0
    digest = "sha256:edc3643ddf96d75032a55e240900b68b335186f1e5fea0a95af3b4cc96020b77",
)

go_repository(
    name = "com_github_golang_protobuf",
    importpath = "github.com/golang/protobuf",
    tag = "v1.2.0",
)

go_repository(
    name = "com_github_google_go_containerregistry",
    commit = "abf9ef06abd9",
    importpath = "github.com/google/go-containerregistry",
)

go_repository(
    name = "com_google_cloud_go",
    importpath = "cloud.google.com/go",
    tag = "v0.34.0",
)

go_repository(
    name = "in_gopkg_check_v1",
    commit = "788fd7840127",
    importpath = "gopkg.in/check.v1",
)

go_repository(
    name = "in_gopkg_yaml_v2",
    importpath = "gopkg.in/yaml.v2",
    tag = "v2.2.2",
)

go_repository(
    name = "org_golang_google_appengine",
    importpath = "google.golang.org/appengine",
    tag = "v1.4.0",
)

go_repository(
    name = "org_golang_x_net",
    commit = "d28f0bde5980",
    importpath = "golang.org/x/net",
)

go_repository(
    name = "org_golang_x_oauth2",
    commit = "0f29369cfe45",
    importpath = "golang.org/x/oauth2",
)

go_repository(
    name = "org_golang_x_sync",
    commit = "37e7f081c4d4",
    importpath = "golang.org/x/sync",
)

go_repository(
    name = "org_golang_x_text",
    importpath = "golang.org/x/text",
    tag = "v0.3.2",
)

go_repository(
    name = "com_github_google_go_cmp",
    importpath = "github.com/google/go-cmp",
    tag = "v0.3.0",
)

go_repository(
    name = "io_k8s_klog",
    importpath = "k8s.io/klog",
    tag = "v0.3.3",
)

go_repository(
    name = "com_github_davecgh_go_spew",
    importpath = "github.com/davecgh/go-spew",
    tag = "v1.1.1",
)

go_repository(
    name = "com_github_docker_spdystream",
    commit = "449fdfce4d96",
    importpath = "github.com/docker/spdystream",
)

go_repository(
    name = "com_github_elazarl_goproxy",
    commit = "c4fc26588b6e",
    importpath = "github.com/elazarl/goproxy",
)

go_repository(
    name = "com_github_emicklei_go_restful",
    commit = "ff4f55a20633",
    importpath = "github.com/emicklei/go-restful",
)

go_repository(
    name = "com_github_evanphx_json_patch",
    importpath = "github.com/evanphx/json-patch",
    tag = "v4.2.0",
)

go_repository(
    name = "com_github_fsnotify_fsnotify",
    importpath = "github.com/fsnotify/fsnotify",
    tag = "v1.4.7",
)

go_repository(
    name = "com_github_ghodss_yaml",
    commit = "73d445a93680",
    importpath = "github.com/ghodss/yaml",
)

go_repository(
    name = "com_github_go_openapi_jsonpointer",
    commit = "46af16f9f7b1",
    importpath = "github.com/go-openapi/jsonpointer",
)

go_repository(
    name = "com_github_go_openapi_jsonreference",
    commit = "13c6e3589ad9",
    importpath = "github.com/go-openapi/jsonreference",
)

go_repository(
    name = "com_github_go_openapi_spec",
    commit = "6aced65f8501",
    importpath = "github.com/go-openapi/spec",
)

go_repository(
    name = "com_github_go_openapi_swag",
    commit = "1d0bd113de87",
    importpath = "github.com/go-openapi/swag",
)

go_repository(
    name = "com_github_gogo_protobuf",
    importpath = "github.com/gogo/protobuf",
    tag = "v1.0.0",
)

go_repository(
    name = "com_github_golang_groupcache",
    commit = "02826c3e7903",
    importpath = "github.com/golang/groupcache",
)

go_repository(
    name = "com_github_google_gofuzz",
    importpath = "github.com/google/gofuzz",
    tag = "v1.0.0",
)

go_repository(
    name = "com_github_google_uuid",
    importpath = "github.com/google/uuid",
    tag = "v1.1.1",
)

go_repository(
    name = "com_github_googleapis_gnostic",
    commit = "0c5108395e2d",
    importpath = "github.com/googleapis/gnostic",
)

go_repository(
    name = "com_github_hashicorp_golang_lru",
    importpath = "github.com/hashicorp/golang-lru",
    tag = "v0.5.0",
)

go_repository(
    name = "com_github_hpcloud_tail",
    importpath = "github.com/hpcloud/tail",
    tag = "v1.0.0",
)

go_repository(
    name = "com_github_json_iterator_go",
    importpath = "github.com/json-iterator/go",
    tag = "v1.1.6",
)

go_repository(
    name = "com_github_kr_pretty",
    importpath = "github.com/kr/pretty",
    tag = "v0.1.0",
)

go_repository(
    name = "com_github_kr_pty",
    importpath = "github.com/kr/pty",
    tag = "v1.1.1",
)

go_repository(
    name = "com_github_kr_text",
    importpath = "github.com/kr/text",
    tag = "v0.1.0",
)

go_repository(
    name = "com_github_mailru_easyjson",
    commit = "d5b7844b561a",
    importpath = "github.com/mailru/easyjson",
)

go_repository(
    name = "com_github_modern_go_concurrent",
    commit = "bacd9c7ef1dd",
    importpath = "github.com/modern-go/concurrent",
)

go_repository(
    name = "com_github_modern_go_reflect2",
    importpath = "github.com/modern-go/reflect2",
    tag = "v1.0.1",
)

go_repository(
    name = "com_github_munnerz_goautoneg",
    commit = "a547fc61f48d",
    importpath = "github.com/munnerz/goautoneg",
)

go_repository(
    name = "com_github_mxk_go_flowrate",
    commit = "cca7078d478f",
    importpath = "github.com/mxk/go-flowrate",
)

go_repository(
    name = "com_github_nytimes_gziphandler",
    commit = "56545f4a5d46",
    importpath = "github.com/NYTimes/gziphandler",
)

go_repository(
    name = "com_github_onsi_ginkgo",
    importpath = "github.com/onsi/ginkgo",
    tag = "v1.8.0",
)

go_repository(
    name = "com_github_onsi_gomega",
    importpath = "github.com/onsi/gomega",
    tag = "v1.5.0",
)

go_repository(
    name = "com_github_pmezard_go_difflib",
    importpath = "github.com/pmezard/go-difflib",
    tag = "v1.0.0",
)

go_repository(
    name = "com_github_puerkitobio_purell",
    importpath = "github.com/PuerkitoBio/purell",
    tag = "v1.0.0",
)

go_repository(
    name = "com_github_puerkitobio_urlesc",
    commit = "5bd2802263f2",
    importpath = "github.com/PuerkitoBio/urlesc",
)

go_repository(
    name = "com_github_spf13_pflag",
    importpath = "github.com/spf13/pflag",
    tag = "v1.0.3",
)

go_repository(
    name = "com_github_stretchr_objx",
    importpath = "github.com/stretchr/objx",
    tag = "v0.1.0",
)

go_repository(
    name = "com_github_stretchr_testify",
    importpath = "github.com/stretchr/testify",
    tag = "v1.3.0",
)

go_repository(
    name = "in_gopkg_fsnotify_v1",
    importpath = "gopkg.in/fsnotify.v1",
    tag = "v1.4.7",
)

go_repository(
    name = "in_gopkg_inf_v0",
    importpath = "gopkg.in/inf.v0",
    tag = "v0.9.0",
)

go_repository(
    name = "in_gopkg_tomb_v1",
    commit = "dd632973f1e7",
    importpath = "gopkg.in/tomb.v1",
)

go_repository(
    name = "io_k8s_apimachinery",
    commit = "0bb8574e0887",
    importpath = "k8s.io/apimachinery",
)

go_repository(
    name = "io_k8s_gengo",
    commit = "0689ccc1d7d6",
    importpath = "k8s.io/gengo",
)

go_repository(
    name = "io_k8s_kube_openapi",
    commit = "33be087ad058",
    importpath = "k8s.io/kube-openapi",
)

go_repository(
    name = "io_k8s_sigs_structured_merge_diff",
    commit = "15d366b2352e",
    importpath = "sigs.k8s.io/structured-merge-diff",
)

go_repository(
    name = "io_k8s_sigs_yaml",
    importpath = "sigs.k8s.io/yaml",
    tag = "v1.1.0",
)

go_repository(
    name = "org_golang_x_crypto",
    commit = "c2843e01d9a2",
    importpath = "golang.org/x/crypto",
)

go_repository(
    name = "org_golang_x_sys",
    commit = "15dcb6c0061f",
    importpath = "golang.org/x/sys",
)

go_repository(
    name = "org_golang_x_tools",
    commit = "1f849cf54d09",
    importpath = "golang.org/x/tools",
)
