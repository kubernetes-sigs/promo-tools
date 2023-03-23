# Container Image Promoter

The Container Image Promoter (aka "CIP") promotes Docker images from one
Registry (src registry) to another (dest registry). The set of images to promote
are defined by promoter manifests, in YAML.

Currently only Google Container Registry (GCR) is supported.

- [Promoting images](#promoting-images)
  - [Promoter manifests](#promoter-manifests)
    - [Plain manifest example](#plain-manifest-example)
    - [Thin manifests example](#thin-manifests-example)
  - [Registries and service accounts](#registries-and-service-accounts)
- [How promotion works](#how-promotion-works)
- [Server-side operations](#server-side-operations)
- [Grabbing snapshots](#grabbing-snapshots)
  - [Snapshots of promoter manifests](#snapshots-of-promoter-manifests)
- [Checks Interface](#checks-interface)

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
  --thin-manifest-dir=<path_to_registry.k8s.io_thin_manifest_dir> \
  --output=csv | wc -l
```

## Checks Interface

Read more [here](./checks.md).

The addition of the checks interface to the Container Image Promoter is meant
to make it easy to add checks against pull requests affecting the promoter
manifests. The interface allows engineers to add checks without worrying about
any pre-existing checks and test their own checks individually, while also
giving freedom as to what conditionals or tags might be necessary for the
check to occur.

[cip-prow-integration]: https://git.k8s.io/k8s.io/k8s.gcr.io/Vanity-Domain-Flip.md#prow-integration
[docker]: https://docs.docker.com/get-docker
[golang]: https://golang.org/doc/install
[k/k8s.io]: https://git.k8s.io/k8s.io
[k/release]: https://git.k8s.io/release
[k/test-infra]: https://git.k8s.io/test-infra
[k8sio-manifests-dir]: https://git.k8s.io/k8s.io/registry.k8s.io
