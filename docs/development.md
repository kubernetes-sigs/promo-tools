# Development

- [Maintenance](#maintenance)
  - [Linting](#linting)
  - [Testing](#testing)
    - [Faking dependencies](#faking-dependencies)
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

#### Faking dependencies

The promoter uses [counterfeiter](https://github.com/maxbrunsfeld/counterfeiter)
to generate fakes for its core interfaces. The `promoterImplementation` interface
in `internal/promoter/image/` and the `registry.Provider` interface in
`promoter/image/registry/` both have generated fakes used by the unit tests.

### Automated builds

The `gcr.io/k8s-staging-artifact-promoter` GCR is a staging repo for Docker
image build artifacts from this project. Every update to the default
development branch of this Github repo results in the `kpromo` image being
built in the staging GCR repo:

1. `gcr.io/k8s-staging-artifact-promoter/kpromo`

This image gets built and pushed by GCB using the [build file
here][cloudbuild.yaml]. There is also a production version:

1. `{asia,eu,us}.gcr.io/k8s-artifacts-prod/artifact-promoter/kpromo`

The image from the staging GCR ends up in `k8s-artifacts-prod` using the
promoter image running in
[Prow](https://github.com/kubernetes/test-infra/tree/master/prow). "Using the
promoter" here means creating a PR in the [k8s.io Github repo][k8sio-manifests-dir]
to promote versions from staging to production, such as in
[this PR](https://github.com/kubernetes/k8s.io/pull/704).

#### Connection with Prow

There are Prow jobs that consume the production `kpromo` container image.
These jobs are defined [here][cip-prow-integration].

If there is a breaking change where the Docker image doesn't work any more for
these Prow jobs, the sequence of events required to fix those Prow jobs are:

1. fix the bug in this codebase
2. generate a new `kpromo` image in
   `gcr.io/k8s-staging-artifact-promoter` (automated)
3. promote the image into production
4. update Prow jobs to use the new image from Step 3

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
