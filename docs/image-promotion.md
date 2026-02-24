# Container Image Promoter

The Container Image Promoter promotes OCI images from a source (staging)
registry to one or more destination (production) registries. The set of images
to promote is defined by promoter manifests in YAML. Image operations use
[crane](https://github.com/google/go-containerregistry/tree/main/cmd/crane)
and work with any OCI-compliant registry.

- [Promoting images](#promoting-images)
  - [Promoter manifests](#promoter-manifests)
    - [Plain manifest example](#plain-manifest-example)
    - [Thin manifests example](#thin-manifests-example)
  - [Registries and service accounts](#registries-and-service-accounts)
- [How promotion works](#how-promotion-works)
  - [Pipeline phases](#pipeline-phases)
  - [Rate limiting](#rate-limiting)
- [Server-side operations](#server-side-operations)
- [Signing and attestation](#signing-and-attestation)
- [Provenance verification](#provenance-verification)
- [Vulnerability scanning](#vulnerability-scanning)
- [Grabbing snapshots](#grabbing-snapshots)
  - [Snapshots of promoter manifests](#snapshots-of-promoter-manifests)

## Promoting images

Using the promoter requires:

1. promoter manifest(s)
2. source registry
3. destination registry
4. service account for writing into the destination registry

### Promoter manifests

A promoter manifest has two sub-fields:

1. `registries`
2. `images`

There are 2 types of manifests, *plain* and *thin*. A plain manifest has both
`registries` and `images` in one YAML file. A thin manifest splits these into
2 separate YAML files. In practice, thin manifests are preferred because they
work better at scale; for example, the [k8s.io repo][k8sio-manifests-dir] only
uses thin manifests because it allows `images` to be easily modified in PRs,
whereas the more sensitive `registries` field remains tightly controlled by a
handful of owners.

#### Plain manifest example

```yaml
registries:
- name: gcr.io/myproject-staging-area # publicly readable
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

The `registries` field lists all destination registries and the source registry
(marked with `src: true`). The promoter scans the source registry and promotes
matching images to each destination.

Given the above manifest:

```console
kpromo cip --manifest=path/to/manifest.yaml
```

To actually perform the promotion (not just a dry run), add `--confirm`:

```console
kpromo cip --manifest=path/to/manifest.yaml --confirm
```

#### Thin manifests example

Use thin manifests by specifying `--thin-manifest-dir=<target directory>`.
The directory structure must be:

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

The folder names (`images`, `manifests`) and filenames (`images.yaml`,
`promoter-manifest.yaml`) are hardcoded. Subdirectory names (`a`, `b`, `c`,
`d`) must match between `images` and `manifests`.

`manifests/a/promoter-manifest.yaml`:

```yaml
registries:
- name: gcr.io/myproject-staging-area
  src: true
- name: gcr.io/myproject-production
  service-account: foo@google-containers.iam.gserviceaccount.com
```

`images/a/images.yaml`:

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

### Registries and service accounts

The promoter needs:

- **source registry**: read access
- **destination registry**: read and write access

In a dry run (default, without `--confirm`), only read access is needed for the
destination registry. Source registries are typically world-readable and don't
need a `service-account` field.

## How promotion works

The promoter's behaviour can be described in terms of mathematical sets.
Suppose `S` is the set of images in the source registry, `D` is the set of all
images in the destination registry, and `M` is the set of images to be promoted
(defined in the manifest). Then:

- `M ∩ D` = images already present in the destination (no action needed)
- `(M ∩ S) \ D` = images that are copied
- `M \ (S ∪ D)` = images that cannot be found (warnings are printed)

### Pipeline phases

The promotion flow is organized into sequential pipeline phases:

| Phase | Name | Description |
|-------|------|-------------|
| 1 | **setup** | Validate options, activate service accounts, prewarm TUF cache |
| 2 | **plan** | Parse manifests, read registry inventories, compute promotion edges |
| 3 | **provenance** | Optional SLSA provenance verification (see [Provenance verification](#provenance-verification)) |
| 4 | **validate** | Validate staging image signatures |
| 5 | **promote** | Copy images from staging to production |
| 6 | **sign** | Sign promoted images with cosign, replicate signatures to mirrors |
| 7 | **attest** | Copy pre-generated SBOMs from staging to production, generate promotion provenance |

Without `--confirm`, the pipeline stops after the validate phase (dry-run
precheck). With `--parse-only`, it stops after parsing manifests.

### Rate limiting

HTTP requests are rate-limited to avoid 429 errors from registry quotas. The
rate limiter covers all HTTP methods (not just reads) and uses adaptive backoff
when 429 responses are received.

The total request budget is split between promotion (70%) and signing (30%).
After the promote phase completes, the full budget is rebalanced to signing.

## Server-side operations

During promotion, all data resides on the server. No images are pulled and
pushed back up. This is important for two reasons:

1. **Performance**: Images can be gigabytes in size.
2. **Digest preservation**: Pulling/pushing can change the digest because layers
   might get gzipped differently. Server-side operations preserve the digest.

## Signing and attestation

After promotion, images are signed using [cosign](https://github.com/sigstore/cosign)
with a keyless (OIDC) identity. Signatures are replicated to all mirror
registries. The signing identity is configured with `--signer-account`.

Pre-generated SBOMs are copied from staging to production using the cosign SBOM
tag convention (`sha256-<hash>.sbom`). Missing SBOMs are skipped gracefully.

Related flags:

- `--sign` — enable/disable signing (default: `true`)
- `--signer-account` — service account identity for signing
- `--certificate-identity` — identity to verify when checking signatures
- `--certificate-oidc-issuer` — OIDC issuer for the signing identity
- `--max-signature-copies` — max concurrent signature copies (default: `50`)
- `--max-signature-ops` — max concurrent signature operations (default: `50`)

## Provenance verification

Optional SLSA provenance verification can be enabled to ensure images were built
by authorized build systems before promotion. When enabled, the promoter checks
that a SLSA attestation tag exists on each staging image and verifies the builder
identity and source repository against configured policies.

Related flags:

- `--require-provenance` — require valid SLSA attestations (default: `false`)
- `--allowed-builders` — comma-separated list of acceptable builder identities
- `--allowed-source-repos` — comma-separated list of acceptable source repo URLs

## Provenance generation

The promoter can generate SLSA v1.0 provenance attestations for promoted images.
When enabled, the promoter pushes an `.att` tag for each promoted image containing
an in-toto statement with the promotion metadata (source/destination registries,
digest, builder identity, timestamp).

```console
kpromo cip --thin-manifest-dir=<dir> --generate-promotion-provenance --confirm
```

Related flags:

- `--generate-promotion-provenance` — generate SLSA provenance attestations (default: `false`)

## Vulnerability scanning

The promoter supports vulnerability scanning of staging images before promotion.
The `--vuln-severity-threshold` flag sets the minimum severity level that causes
the scan to fail (0=UNSPECIFIED through 5=CRITICAL). See [checks](./checks.md)
for details.

## Grabbing snapshots

The promoter can generate textual snapshots of all images in a registry. Such
snapshots provide a lightweight "fingerprint" of a registry and can be used to
generate the `images` part of a thin manifest.

To snapshot a registry:

```console
kpromo cip --snapshot=gcr.io/foo
```

This outputs YAML compatible with thin manifests' `images.yaml` format. Use
`--output=csv` for CSV format:

```console
kpromo cip --snapshot=gcr.io/foo --output=csv
```

The `--minimal-snapshot` flag discards tagless child images that are referenced
by manifest lists, making the output lighter.

### Snapshots of promoter manifests

You can snapshot a destination registry defined in thin manifest directories
with `--manifest-based-snapshot-of`. This is useful for getting a unified view
of a destination registry that is split across multiple thin manifests:

```console
kpromo cip \
  --manifest-based-snapshot-of=us.gcr.io/k8s-artifacts-prod \
  --thin-manifest-dir=<path_to_thin_manifest_dir> \
  --output=csv | wc -l
```

[k8sio-manifests-dir]: https://git.k8s.io/k8s.io/registry.k8s.io
