# Container Image Promoter

The Container Image Promoter (aka "CIP") promotes Docker images from one
Registry (src registry) to another (dest registry). The set of images to promote
are defined by promoter manifests, in YAML.

Currently only Google Container Registry (GCR) is supported.

- [Install](#install)
- [Promoting images](#promoting-images)
  - [Promoter manifests](#promoter-manifests)
    - [Plain manifest example](#plain-manifest-example)
    - [Thin manifests example](#thin-manifests-example)
  - [Registries and service accounts](#registries-and-service-accounts)
- [How promotion works](#how-promotion-works)
- [Server-side operations](#server-side-operations)
- [Grabbing snapshots](#grabbing-snapshots)
  - [Snapshots of promoter manifests](#snapshots-of-promoter-manifests)
- [Maintenance](#maintenance)
  - [Linting](#linting)
  - [Testing](#testing)
    - [Faking http calls and shell processes](#faking-http-calls-and-shell-processes)
  - [Automated builds](#automated-builds)
    - [Connection with Prow](#connection-with-prow)
- [Versioning](#versioning)
  - [Default versioning](#default-versioning)
- [Checks Interface](#checks-interface)
- [Vulnerability Dashboard](#vulnerability-dashboard)

## Install

1. Install [bazel][bazel].
2. Run the steps below:

```console
go get sigs.k8s.io/k8s-container-image-promoter
cd $GOPATH/src/sigs.k8s.io/k8s-container-image-promoter

# Install the "cip" binary into $GOPATH/bin
make install
```

## Promoting images

Using CIP to promote images requires four pieces:

1. promoter manifest(s)
2. source registry
3. destination registry
4. service account for writing into destination registry

### Promoter manifests

A promoter manifest has two sub-fields:

1. `registries`
2. `images`

In addition there are 2 types of manifests, *plain* and *thin*. The difference
is in how they are written, not their content. A plain manifest has both
`registries` and `images` in one YAML file. On the other hand, a thin manifest
splits these two fields up into 2 separate YAML files. In practice, thin
manifests are preferred because they work better when modifying these YAMLs at
scale; for example, the [k8sio-manifests-dir][k8s.io Github repo] only uses thin
manifests because it allows `images` to be easily modified in PRs, whereas the
more sensitive `registries` field remains tightly controlled by a handful of
owners.

#### Plain manifest example

```yaml
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

Here, the manifest cares about 3 images --- `apple`, `banana`, and `cherry`. The
`registries` field lists all destination registries and also the source registry
where the images should be promoted from. To earmark the source registry, it has
`src: true` as a property. The promoter will then scan
`gcr.io/myproject-staging-area` and promote the images found under `images` to
`gcr.io/myproject-production`.

The source registry will always be read-only for the promoter. Because of this,
it's OK to not provide a `service-account` field for it in `registries`. But in
the event that you are trying to promote from one private registry to another,
you would still provide a `service-account` for the staging registry.

Given the above manifest, you can run CIP as follows:

```console
cip run --manifest=path/to/manifest.yaml
```

#### Thin manifests example

You can use these thin manifests by specifying the `--thin-manifest-dir=<target
directory>` flag, which forces all promoter manifests to be defined as thin
manifests within the target directory. This is the only flag that currently
supports thin manifests.

Assume `foo` is the `<target directory>`. The structure of `foo` must be as follows:

```console
foo
├── images
│   ├── a
│   │   └── images.yaml
│   ├── b
│   │   └── images.yaml
│   ├── c
│   │   └── images.yaml
│   └── d
│       └── images.yaml
└── manifests
    ├── a
    │   └── promoter-manifest.yaml
    ├── b
    │   └── promoter-manifest.yaml
    ├── c
    │   └── promoter-manifest.yaml
    └── d
        └── promoter-manifest.yaml
```

Here there are 4 thin promoter manifests. The folder names (`images`,
`manifests`) and filenames (`images.yaml`, `promoter-manifest.yaml`) are
hardcoded and cannot be changed. The only things that may be changed are the
folder names `foo` and the subdirectory names (`a`, `b`, `c`, `d`) under
`images` and `manifests`. That being said, the subdirectory names (`a`, `b`,
`c`, `d`) must match in `images` and `manifests`; otherwise, CIP will exit with
an error.

Continuing with the example plain manifest in the previous section, let's
pretend we wanted to convert it into a thin manifest. Let's use subdirectory `a`
as an example. First, `manifests/a/promoter-manifest.yaml` would look like this:

```yaml
registries:
- name: gcr.io/myproject-staging-area # publicly readable, does not need a service account for access
  src: true # mark it as the source registry (required)
- name: gcr.io/myproject-production
  service-account: foo@google-containers.iam.gserviceaccount.com
```

Second, the images would be put in `images/a/images.yaml` like this:

```yaml
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

It's important to note that the subdirectory name `a` here could be renamed to
anything else allowed by the filesystem --- it has no bearing on the substantive
contents of the promoter manifest. Indeed, the subdirectory name only acts as an
organizing namespace to separate it from the other subdirectory names that might
exist (in the example `b`, `c`, and `d`).

### Registries and service accounts

CIP needs the following access to registries:

- source registry: read access
- destination registry: read and write access

In a dry run (default), where CIP does not actually perform any image copies,
only read access in needed for the destination registry.

For *source registries*, most commonly they are world-readable. Hence, no
`service-account` field is needed for them. However, if your source registry is
not world-readable, you would need to define a `service-account` field and
specify the name of the service account that has read access.

For *destination registries*, most commonly they are world-readable, but not
world-writable. To allow CIP to write to such registries, they require a
`service-account` field. This is the case of the examples shown thus far, where
`foo@google-containers.iam.gserviceaccount.com` presumably has write access to
`gcr.io/myproject-production`.

When CIP starts up, it associates any defined `service-account`s to the
registries, and also reads in a temporary service account token, and binds it to
the corresponding registry. The credentials for these service accounts must
already be set up in the environment prior to running the promoter.

## How promotion works

The promoter's behaviour can be described in terms of mathematical sets (as in Venn diagrams).
Suppose `S` is the set of images in the source registry, `D` is the set of all images in the destination registry and
`M` is the set of images to be promoted (these are defined in the promoter manifest). Then:

- `M ∩ D` = images which do not need promoting since they are already present in the destination registry
- `(M ∩ S) \ D` = images that are copied

The above statements are true for each destination registry.

The promoter also prints warnings about images that cannot be promoted:

- `M \ (S ∪ D)` = images that cannot be found

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

## Grabbing snapshots

The promoter can also be used to quickly generate textual snapshots of all
images found in a registry. Such snapshots provide a kind of lightweight
"fingerprint" of a registry, and are useful in comparing registries. The
snapshots can also be used to generate the `images` part of a thin manifest, if
you want to promote *all* images from one registry to another.

To snapshot a GCR registry, you can do:

```console
cip run --snapshot=gcr.io/foo
```

which will output YAML that is compatible with thin manifests' `images.yaml`
format. You can also force CSV format by specifying the `--output=csv`
flag:

```console
cip run --snapshot=gcr.io/foo --output=csv
```

which will output a CSV of image digests and tags found at `gcr.io/foo`.

There is another option, `--minimal-snapshot`, which will discard all tagless
child images that are referenced by Docker manifest lists (manifest lists are
Docker images that specify a group of related Docker images, usually one image
per machine architecture). That is, if there is a Docker manifest list that
references 10 child images, and these child images are not tagged, then they are
discarded from the snapshot output with `--minimal-snapshot`. This makes the
resulting output lighter by removing redundant information.

### Snapshots of promoter manifests

Apart from GCR registries, you can also snapshot a destination registry defined
in thin manifest directories, with the `--manifest-based-snapshot-of` flag. This
is useful if for example you want to have a unified look at a particular
destination registry that is broken up over multiple thin manifests. For
example, the thin manifests defined [k8sio-manifests-dir][here] all promoter to
3 registries, `{asia,eu,us}.gcr.io/k8s-artifacts-prod`. But the various
subdirectories there all promote images into one of these three registries. From
examining the thin manifests by hand, it can be difficult to answer questions
such as, "how many total images (counting by unique digests) are we promoting
into `gcr.io/k8s-artifacts-prod`?"

We can answer the above question with:

```console
cip \
  --manifest-based-snapshot-of=us.gcr.io/k8s-artifacts-prod \
  --thin-manifest-dir=<path_to_k8s.gcr.io_thin_manifest_dir> \
  --output=csv | wc -l
```

## Maintenance

### Linting

We use [golangci-lint](https://github.com/golangci/golangci-lint); please
install it and run `make lint` to check for linting errors. There should be 0
linting errors; if any should be ignored, add a line ignoring the error with a
`//nolint[:linter1,linter2,...]`
[directitve](https://github.com/golangci/golangci-lint#false-positives). Grep
for `nolint` in this repo for examples.

### Testing

Run `make test`; this will invoke a bazel rule to run all unit tests. By
default, running `make` alone also invokes the same tests.

Every critical piece has a unit test --- unit tests complete nearly instantly,
so you should *always* add unit tests where possible, and also run them before
submitting a new PR.

#### Faking http calls and shell processes

As the promoter uses a combination of network API calls and shell-instantiated
processes, we have to fake them for the unit tests. To make this happen, these
mechanisms all use a `stream.Producer` [interface](lib/stream/types.go). The
real-world code uses either the [http](lib/stream/http.go) or
[subprocess](lib/stream/subprocess.go) implementations of this interface to
create streams of data (JSON or not) which we can interpret and use.

For tests, the [fake](lib/stream/fake.go) implementation is used instead, which
predefines how that stream will behave, for the purposes of each unit test. A
good example of this is the [`TestReadRegistries`
test](lib/dockerregistry/inventory_test.go).

### Automated builds

The `gcr.io/k8s-staging-artifact-promoter` GCR is a staging repo for Docker
image build artifacts from this project. Every update to the `master` branch in
this Github repo results in a new set of 2 images in the staging GCR repo:

1. `gcr.io/k8s-staging-artifact-promoter/cip`
1. `gcr.io/k8s-staging-artifact-promoter/cip-auditor`

These images get built and pushed up there by GCB using the [build file
here][cloudbuild.yaml]. There are also production versions of these images here:

1. `{asia,eu,us}.gcr.io/k8s-artifacts-prod/artifact-promoter/cip`
1. `{asia,eu,us}.gcr.io/k8s-artifacts-prod/artifact-promoter/cip-auditor`

The images from the staging GCR end up in `k8s-artifacts-prod` using the
promoter image running in
[Prow](https://github.com/kubernetes/test-infra/tree/master/prow). "Using the
promoter" here means creating a PR in the [k8sio-manifests-dir][k8s.io Github
repo] to promote versions from staging to production, such as in [this
PR](https://github.com/kubernetes/k8s.io/pull/704).

#### Connection with Prow

There are a number of Prow jobs that consume the production Docker images of
`cip` or `cip-auditor`. These jobs are defined [cip-prow-integration][here].

The important thing to note is that ultimately the jobs there are downstream
consumers of the production `cip` and `cip-auditor` images discussed above. So
if there is a breaking change where the Docker images don't work any more for
these Prow jobs, the sequence of events required to fix those Prow jobs are:

1. fix the bug in this codebase
2. generate new `cip` and `cip-auditor` images in `gcr.io/k8s-staging-artifact-promoter` (automated)
3. promote images into production
4. update Prow jobs to use the new images from Step 3

Step 1 is done in this Github repo. Step 3 is done in [the k8s.io Github
repo](https://github.com/kubernetes/k8s.io/tree/main/k8s.gcr.io). Step 4 is
done in the [test-infra Github repo](https://github.com/kubernetes/test-infra).

## Versioning

We follow [Semver](https://semver.org/) for versioning. For each new release,
create a new release on GitHub with:

- Update VERSION file to bump the semver version (e.g., `1.0.0`)
- Create a new commit for the 1-liner change above with this command with `git commit -m "cip 1.0.0"`
- Create an annotated tag at this point with `git tag -a "v1.0.0" -m "cip 1.0.0"`
- Push this version to the `master` branch (requires write access)

### Default versioning

The Docker images that are produced by this repo are automatically tagged in the
following format: `YYYYMMDD-<git-describe>`. As such, there is no need to bump
the VERSION file often as the Docker images will always get a unique identifier.

[bazel]:https://bazel.build/
[k8sio-manifests-dir]:https://github.com/kubernetes/k8s.io/tree/main/k8s.gcr.io
[cip-prow-integration]:https://github.com/kubernetes/k8s.io/blob/main/k8s.gcr.io/Vanity-Domain-Flip.md#prow-integration

## Checks Interface

Read more [here](https://github.com/kubernetes-sigs/k8s-container-image-promoter/blob/master/checks_interface.md).

The addition of the checks interface to the Container Image Promoter is meant
to make it easy to add checks against pull requests affecting the promoter
manifests. The interface allows engineers to add checks without worrying about
any pre-existing checks and test their own checks individually, while also
giving freedom as to what conditionals or tags might be necessary for the
check to occur.

## Vulnerability Dashboard

The vulnerability dashboard (`vulndash`) has moved to [`kubernetes/release`][k/release].

Read more [here][vulndash-readme].

[k/release]: https://git.k8s.io/release
[vulndash-readme]: https://git.k8s.io/release/docs/vuln-dashboard.md
