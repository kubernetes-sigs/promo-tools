# Container Image Promoter

The Container Image Promoter (aka "cip") promotes images from one Docker
Registry (src registry) to another (dest registry), by reading a Manifest file
(in YAML). The Manifest lists Docker images, and all such images are considered
"blessed" and will be copied from src to dest.

Example Manifest for images:

```
registries:
- name: gcr.io/myproject-staging-area # publicly readable, does not need a service account for access
  src: true # mark it as the source registry (required)
- name: gcr.io/myproject-production
  service-account: foo@google-containers.iam.gserviceaccount.com
images:
- name: apple
  dmap:
    "sha256:e8ca4f9ff069d6a35f444832097e6650f6594b3ec0de129109d53a1b760884e9": ["1.1", "latest"]
- name: banana
  dmap:
    "sha256:c3d310f4741b3642497da8826e0986db5e02afc9777a2b8e668c8e41034128c1": ["1.0"]
- name: cherry
  dmap:
    "sha256:ec22e8de4b8d40252518147adfb76877cb5e1fa10293e52db26a9623c6a4e92b": ["1.0"]
    "sha256:06fdf10aae2eeeac5a82c213e4693f82ab05b3b09b820fce95a7cac0bbdad534": ["1.2", "latest"]
```

Here, the Manifest cares about 3 images --- `apple`, `banana`, and `cherry`. The
`registries` field lists all destination registries and also the source registry
where the images should be promoted from. To earmark the source registry, it is
called out on its own under the `src-registry` field. In the Example, the
promoter will scan `gcr.io/myproject-staging-area` (*src-registry*) and promote
the images found under `images` to `gcr.io/myproject-production`.

The `src-registry` (staging registry) will always be read-only for the promoter.
Because of this, it's OK to not provide a `service-account` field for it in
`registries`. But in the event that you are trying to promote from one private
registry to another, you would still provide a `service-account` for the staging
registry.

Currently only Google Container Registry (GCR) is supported.

## Renames

It is possible to rename images during the process of promotion. For example, if
you want `gcr.io/myproject-staging-area/apple` to get promoted as
`gcr.io/myproject-production/some/other/subdir/apple`, you could add the
following to the Example above:

```
renames:
- ["gcr.io/myproject-staging-area/apple", "gcr.io/myproject-production/some/other/subdir/apple"]
```

Each entry in the `renames` field is a list of image paths; all images in the
list are treated as "equal". The only requirement is that each list must contain
at least 1 item that points to a source registry (in this case,
`gcr.io/myproject-staging-area`).

# Install

1. Install [bazel][bazel].
2. Run the steps below:

```
go get sigs.k8s.io/k8s-container-image-promoter
cd $GOPATH/src/sigs.k8s.io/k8s-container-image-promoter
make build
```

# Running the Promoter

The promoter relies on calls to `gcloud container images ...` to realize the
intent of the Manifest. It also tries to run the command as the account in
`service-account`. The credentials for this service account must already be set
up in the environment prior to running the promoter.

Given the Example Manifest as above, you can run the promoter with:

```
bazel run -- cip -h -verbosity=3 -manifest=path/to/manifest.yaml
```

Alternatively, you can run the binary directly by examining the bazel output
from running `make build`, and then invoking it with the correct path under
`./bazel-bin`. For example, if you are on a Linux machine, running `make build`
will output a binary at `./bazel-bin/linux_amd64_stripped/cip`.

# How it works

At a high level, the promoter simply performs set operations ("set" as in
mathematics, like in Venn diagrams). It first creates the set of all images in
the destination registry (let's call it `D`). It also creates the set of all
images defined in the promoter manifest as "promotion candidates in the
manifest" (call it `M`).

Given the above, we get the following results:

- `M ∩ D` = images that have already been promoted (NOP)
- `M \ D` = images that must be promoted
- `D \ M` = images that are extraneous to the manifest (can be deleted with `-delete-extra-tags` flag)

If there are multiple destination registries, the above calculation is repeated
for each destination registry. The promoter also prints warnings about images in
`M` that cannot be found in the source registry (call it `S`):

- `M \ S` = images that are lost (no way to promote it because it cannot be found!)

## Server-side operations

During the promotion process, all data resides on the server (currently, Google
Container Registry for images). That is, no images get pulled and pushed back
up. There are two reasons why it does things entirely server-side:

1. Performance. Images can be gigabytes in size and it would take forever to
   pull/push images in their entirety for every promotion.
1. Digest preservation. Pulling/pushing the images can change their digest
   (sha256sum) because layers might get gzipped differently when they are pushed
   back up. Doing things entirely server-side preserves the digest, which is
   important for declaratively recording the images by their digest in the
   promoter manifest.

# Maintenance

## Linting

We use [golangci-lint](https://github.com/golangci/golangci-lint); please
install it and run `make lint` to check for linting errors. There should be 0
linting errors; if any should be ignored, add a line ignoring the error with a
`//nolint[:linter1,linter2,...]`
[directitve](https://github.com/golangci/golangci-lint#false-positives). Grep
for `nolint` in this repo for examples.

## Testing

Run `make test`; this will invoke a bazel rule to run all unit tests.

Every critical piece has a unit test --- unit tests complete nearly instantly,
so you should *always* add unit tests where possible, and also run them before
submitting a new PR. There is an open issue to make
[linting](https://github.com/kubernetes-sigs/k8s-container-image-promoter/issues/36)
and
[unit/e2e](https://github.com/kubernetes-sigs/k8s-container-image-promoter/issues/8)
tests part of a Prow job, to guard against human error in this area.

### Faking http calls and shell processes

As the promoter uses a combination of network API calls and shell-instantiated
processes, we have to fake them for the unit tests. To make this happen, these
mechanisms all use a `stream.Producer` [interface](lib/stream/types.go). The
real-world code uses either the [http](lib/stream/http.go) or
[subprocess](lib/stream/subprocess.go) implementations of this interface to
create streams of data (JSON or not) which we can interpret and use.

For tests, the [fake](lib/stream/fake.go) implementation is used instead, which
predefines how that stream will behave, for the purposes of each unit test. A
good example of this is the [`TestReadAllRegistries`
test](lib/dockerregistry/inventory_test.go).

## Updating Prow Jobs

Currently there are 3 Prow jobs that use the promoter Docker images. All of
these jobs watch the promoter manifests that live in the [k8s.io
repo](https://github.com/kubernetes/k8s.io/tree/master/k8s.gcr.io). They are:

1. [presubmit job][prow-presubmit-definition] (there is no testgrid entry for this job)
1. [postsubmit job][prow-trusted-definitions] (grep for `post-k8sio-cip`)
1. [daily job][prow-trusted-definitions] (grep for `ci-k8sio-cip`)

The postsubmit and daily jobs also have testgrid entries
([postsubmit](https://k8s-testgrid.appspot.com/sig-release-misc#post-k8sio-cip),
[daily](https://k8s-testgrid.appspot.com/sig-release-misc#ci-k8sio-cip)).

Every time a PR lands against one of the promoter manifests in the
`kubernetes/k8.sio` repo, the presubmit runs, followed by the postsubmit if the
PR gets merged. The daily job (`ci-k8sio-cip`) runs every day as a sanity check
to make sure that both the promoter configuration is correct in the Prow job,
and that the registries have not been tampered with independent of the image
promotion process (e.g., if an image gets promoted out-of-band, then the
promoter will print a warning about it being present).

# Releasing

We follow [Semver](https://semver.org/) for versioning. For each new release,
create a new release on GitHub with:

- Update VERSION file to bump the semver version (e.g., `1.0.0`)
- Create a new commit for the 1-liner change above with this command with `git commit -m "cip 1.0.0"`
- Create an annotated tag at this point with `git tag -a "v1.0.0" -m "cip 1.0.0"`
- Push this version to the `master` branch (requires write access)

We also have to publish the Docker images. Currently they are pushed up to the
`gcr.io/cip-demo-staging` registry (the home will [change to a more official
place](https://github.com/kubernetes-sigs/k8s-container-image-promoter/issues/49)
in the future). To publish them into `gcr.io/cip-demo-staging`, run

- `make image-push` (requires push access to gcr.io/cip-demo-staging)

Once the images are published, you should bump the image tags as they are
referenced in the Prow Jobs by making a PR against the
[test-infra](https://github.com/kubernetes/test-infra/) repo.

[bazel]:https://bazel.build/
[prow-presubmit-definition]:https://github.com/kubernetes/test-infra/blob/master/config/jobs/kubernetes/sig-release/cip/container-image-promoter.yaml
[prow-trusted-definitions]:https://github.com/kubernetes/test-infra/blob/master/config/jobs/kubernetes/test-infra/test-infra-trusted.yaml
