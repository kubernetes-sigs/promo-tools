# Development

- [Maintenance](#maintenance)
  - [Linting](#linting)
  - [Testing](#testing)
    - [Faking http calls and shell processes](#faking-http-calls-and-shell-processes)
  - [Automated builds](#automated-builds)
    - [Connection with Prow](#connection-with-prow)
- [Versioning](#versioning)
  - [Default versioning](#default-versioning)

## Maintenance

### Linting

We use [golangci-lint](https://github.com/golangci/golangci-lint); please
install it and run `make lint` to check for linting errors. There should be 0
linting errors; if any should be ignored, add a line ignoring the error with a
`//nolint[:linter1,linter2,...]`
[directitve](https://github.com/golangci/golangci-lint#false-positives). Grep
for `nolint` in this repo for examples.

### Testing

Run `make test`; this will invoke a script to run all unit tests. By
default, running `make` alone also invokes the same tests.

Every critical piece has a unit test --- unit tests complete nearly instantly,
so you should *always* add unit tests where possible, and also run them before
submitting a new PR.

#### Faking http calls and shell processes

As the promoter uses a combination of network API calls and shell-instantiated
processes, we have to fake them for the unit tests. To make this happen, these
mechanisms all use a `stream.Producer` [interface](/internal/legacy/stream/types.go). The
real-world code uses either the [http](/internal/legacy/stream/http.go) or
[subprocess](/internal/legacy/stream/subprocess.go) implementations of this interface to
create streams of data (JSON or not) which we can interpret and use.

For tests, the [fake](/internal/legacy/stream/fake.go) implementation is used instead, which
predefines how that stream will behave, for the purposes of each unit test. A
good example of this is the [`TestReadRegistries`
test](/internal/legacy/dockerregistry/inventory_test.go).

### Automated builds

The `gcr.io/k8s-staging-artifact-promoter` GCR is a staging repo for Docker
image build artifacts from this project. Every update to the default
development branch of this Github repo results in three images being built in
the staging GCR repo:

1. `gcr.io/k8s-staging-artifact-promoter/cip`
1. `gcr.io/k8s-staging-artifact-promoter/cip-auditor`
1. `gcr.io/k8s-staging-artifact-promoter/kpromo`

These images get built and pushed up there by GCB using the [build file
here][cloudbuild.yaml]. There are also production versions of these images here:

1. `{asia,eu,us}.gcr.io/k8s-artifacts-prod/artifact-promoter/cip`
1. `{asia,eu,us}.gcr.io/k8s-artifacts-prod/artifact-promoter/cip-auditor`
1. `{asia,eu,us}.gcr.io/k8s-artifacts-prod/artifact-promoter/kpromo`

The images from the staging GCR end up in `k8s-artifacts-prod` using the
promoter image running in
[Prow](https://github.com/kubernetes/test-infra/tree/master/prow). "Using the
promoter" here means creating a PR in the [k8s.io Github repo][k8sio-manifests-dir]
to promote versions from staging to production, such as in
[this PR](https://github.com/kubernetes/k8s.io/pull/704).

#### Connection with Prow

There are a number of Prow jobs that consume the production container images
of `cip`, `cip-auditor`, or `kpromo`. These jobs are defined
[here][cip-prow-integration].

The important thing to note is that ultimately the jobs there are downstream
consumers of the production `cip` and `cip-auditor` images discussed above. So
if there is a breaking change where the Docker images don't work any more for
these Prow jobs, the sequence of events required to fix those Prow jobs are:

1. fix the bug in this codebase
2. generate new `cip` and `cip-auditor` images in
   `gcr.io/k8s-staging-artifact-promoter` (automated)
3. promote images into production
4. update Prow jobs to use the new images from Step 3

Step 1 is done in this Github repo. Step 3 is done in [the k8s.io Github
repo][k/k8s.io].

Step 4 is done in the [test-infra Github repo][k/test-infra].

## Versioning

We follow [SemVer](https://semver.org/) for versioning. For each new release,
create a new release on GitHub with:

- Update VERSION file to bump the semver version (e.g., `1.0.0`)
- Create a new commit for the 1-liner change above with this command with
  `git commit --signoff -m "v1.0.0: Release commit"`
- Create a signed tag at this point with `git tag -s -m "v1.0.0" "v1.0.0"`
- Push this version to the default development branch (requires write access)

### Default versioning

The Docker images that are produced by this repo are automatically tagged in the
following format: `YYYYMMDD-<git-describe>`. As such, there is no need to bump
the VERSION file often as the Docker images will always get a unique identifier.

[cip-prow-integration]: https://git.k8s.io/k8s.io/k8s.gcr.io/Vanity-Domain-Flip.md#prow-integration
[k/k8s.io]: https://git.k8s.io/k8s.io
[k/test-infra]: https://git.k8s.io/test-infra
[k8sio-manifests-dir]: https://git.k8s.io/k8s.io/k8s.gcr.io
