# Container Image Promoter

The Container Image Promoter promotes OCI images from a source (staging)
registry to one or more destination (production) registries. The set of images
to promote is defined by promoter manifests in YAML. Image operations use
[crane](https://github.com/google/go-containerregistry/tree/main/cmd/crane).
Registry inventory reads use crane's Google-specific extensions
([`pkg/v1/google`][ggcr-google]), which rely on the GCR/Artifact Registry
tags-list API, so the promoter does not work with arbitrary OCI-compliant
registries.

- [Promoting images](#promoting-images)
  - [Promoter manifests](#promoter-manifests)
    - [Plain manifest example](#plain-manifest-example)
    - [Thin manifests example](#thin-manifests-example)
  - [Registries and service accounts](#registries-and-service-accounts)
- [How promotion works](#how-promotion-works)
  - [Pipeline phases](#pipeline-phases)
  - [Rate limiting](#rate-limiting)
- [Image copying](#image-copying)
- [Signing and attestation](#signing-and-attestation)
- [Provenance verification](#provenance-verification)
- [Provenance generation](#provenance-generation)
- [Checking signatures and attestations](#checking-signatures-and-attestations)
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
| 1 | **setup** | Validate options, prewarm TUF cache |
| 2 | **plan** | Parse manifests, read registry inventories, compute promotion edges |
| 3 | **provenance** | Verify build-time provenance attestations (verify-if-present, see [Provenance verification](#provenance-verification)) |
| 4 | **validate** | Validate staging image signatures |
| 5 | **promote** | Copy images from staging to production |
| 6 | **sign** | Sign promoted images with cosign (primary registry only) |
| 7 | **attest** | Generate promotion provenance attestations |

Without `--confirm`, the pipeline stops after the validate phase (dry-run
precheck). With `--parse-only`, it stops after parsing manifests.

### Rate limiting

HTTP requests are rate-limited to avoid 429 errors from registry quotas. The
whole pipeline uses a single rate limiter (50 requests per second, burst of
5). The rate limiter covers all HTTP methods (not just reads) and uses
adaptive backoff when 429 responses are received.

The limiter applies to image copies (`crane.Copy`) and copies of attached
signature/attestation artifacts, but not to registry inventory reads (which
use the Google-specific `google.Walk`/`google.List` API directly) nor to
cosign's calls to sigstore services during signing.

## Image copying

Promotion copies are performed with `crane.Copy`, which streams the image
manifest and blobs from the source registry to the destination registry
through the promoter process. Copy operations are therefore *not*
server-side: the source registry never transfers data to the destination
registry directly.

- **Digest preservation**: crane forwards the original manifest and layer
  bytes unchanged, so the digest is preserved. Re-encoding layers (for
  example by gzipping them differently) would change the digest, which is
  why images are never unpacked and repacked by the promoter.
- **Performance**: every destination receives a full copy of the image,
  which can be gigabytes in size.

## Signing and attestation

After promotion, images are signed using [cosign](https://github.com/sigstore/cosign)
with a keyless (OIDC) identity. For destinations under the Kubernetes
production path (`k8s-artifacts-prod/images`), signatures use the production
(`registry.k8s.io`) reference of the image as their subject. When the
canonical registry (`us-central1-docker.pkg.dev`) is among the promotion
candidates, signatures are pushed there and served globally through
registry.k8s.io via the `SIGNATURE_UPSTREAM_ENDPOINT` routing in archeio.
The signing identity is configured with `--signer-account`.

Promotion provenance attestations are signed into sigstore bundles and
attached as OCI 1.1 referrer artifacts (cosign's "new bundle format") — no
`.att` or other tags are created for them. One attestation is written per
signing identity and digest, covering all regions and tags of that digest,
including digests promoted without a tag. For destinations under the
Kubernetes production path it is always written to the canonical registry
(`us-central1-docker.pkg.dev`), whether or not the canonical registry is a
promotion candidate, and uses the production (`registry.k8s.io`) reference
as the attestation subject, because sigstore bundles carry no docker
reference.
Attestations are signed with the same identity token flow as image
signatures: the token obtained for `--signer-account` is the only
credential source. The referrer manifest carries the predicate type in
its `dev.sigstore.bundle.predicateType` annotation
(`https://k8s.io/promo-tools/promotion/v1`), which distinguishes promoter
attestations from build-time attestations and makes attesting idempotent:
when a referrer with the promoter predicate type already exists for a
destination digest, it is not attested again.

Related flags:

- `--sign` — enable/disable signing (default: `true`)
- `--signer-account` — service account identity for signing
- `--certificate-identity` — identity to verify when checking signatures
- `--certificate-identity-regexp` — Go regex alternative to
  `--certificate-identity`
- `--certificate-oidc-issuer` — OIDC issuer for the signing identity
- `--certificate-oidc-issuer-regexp` — Go regex alternative to
  `--certificate-oidc-issuer`
- `--max-signature-ops` — max concurrent signature operations (default: `50`)

## Provenance verification

The promoter verifies build-time (SLSA) provenance attestations on staging
images before promotion using verify-if-present semantics: if an attestation
tag exists on a staging image (the legacy cosign `.att` tag convention used
by the staging builds), it is verified once per source digest using cosign
against the SLSA provenance v1 predicate type and the configured signing
identity (`--certificate-identity` or `--certificate-identity-regexp`) and
OIDC issuer (`--certificate-oidc-issuer` or
`--certificate-oidc-issuer-regexp`). The regular expression flags take
precedence over the exact ones. If no attestation is found, a warning is
logged and the image is still promoted. This allows progressive adoption
without blocking images that do not yet have attestations.

Attestations signed by another identity, or with another predicate type,
are logged and ignored. Promotion is blocked only when an attestation does
not verify at all, which means it is malformed or was tampered with.
Keyless attestations are supported; a third party attestation signed with a
key still blocks promotion until per-project identities are supported
([#1952][issue-1952]).

## Provenance generation

The promoter generates a promotion record attestation per signing identity
and digest (see [Signing and attestation](#signing-and-attestation)): an in-toto
statement with the `https://k8s.io/promo-tools/promotion/v1` predicate type
recording the promotion metadata (source and destination, digest, tags,
manifest and commit, promoter version, Prow job and timestamp, see the
[predicate specification](./promotion-predicate.md)). The statement is signed
into a sigstore bundle
and attached to the destination digest through the OCI referrers API as
described in [Signing and attestation](#signing-and-attestation).
Attestations can be verified with
`cosign verify-attestation --new-bundle-format`.

## Checking signatures and attestations

Signing and attesting happen after the images are copied. If they fail, a
rerun of the promotion skips the affected images because they already exist
in production. `kpromo sigcheck` finds and repairs them:

```console
kpromo sigcheck --from-days=7
```

It lists the images uploaded to the canonical registry
(`us-central1-docker.pkg.dev/k8s-artifacts-prod/images`) in the date range
set with `--from-days` and `--to-days`, or checks the image references passed
as arguments (`registry.k8s.io/…` or `*-docker.pkg.dev/k8s-artifacts-prod/images/…`).
Only the canonical registry is checked, because signatures and attestations
are not copied to the other regions and registry.k8s.io serves them from
there. For each image it checks that:

- the image has a signature with a certificate of the expected identity for
  its `registry.k8s.io` reference and digest. Digests promoted without a tag
  are not signed by the promoter, so this is only checked for images with
  tags.
- the digest has a promotion attestation (see
  [Provenance generation](#provenance-generation)) with a certificate of the
  expected identity, whose subject is the `registry.k8s.io` reference and the
  digest. Tagged images and digests promoted without a tag are checked,
  except for the children of an index.

Promotion writes attestations since kpromo v4.6.0, so attestations are only
checked for images uploaded since `--attestations-since` (`YYYY-MM-DD`, UTC).
The default is `2026-09-24`: v4.6.0 was rolled out to the production promotion
jobs on 2026-09-23 at 20:22 UTC, and first promoted images on 2026-09-24.
Older images are only checked for their signature: their missing attestations
are neither reported nor repaired, and digests promoted without a tag are not
checked at all. This keeps `--confirm` from writing incomplete promotion
records for all older images, which promotion would never replace. An earlier
date, or an empty value, extends the attestation checks and repairs to older
images.

The expected identity is set with `--certificate-identity` or
`--certificate-identity-regexp` and `--certificate-oidc-issuer` or
`--certificate-oidc-issuer-regexp`; as in promotion, the regular expressions
take precedence. The default is the identity the promoter signs with. The
certificates are matched but not verified against the sigstore trust root; use
`cosign verify` and `cosign verify-attestation` for that. Signatures,
attestations and other sigstore artifacts (the `.sig`, `.att` and `.sbom`
tags and sigstore bundle referrers) are not checked as images.

Without `--confirm`, `kpromo sigcheck` fails when it finds a problem. With
`--confirm`, it signs and attests the affected images on the canonical
registry with the identity of `--signer-account`, through the same code as
promotion: signatures use the `registry.k8s.io` reference and cover the
children of an index, and one attestation is written per digest. The staging
image and the manifest are not known to `kpromo sigcheck`, so its promotion
records do not include them (see the [predicate](./promotion-predicate.md)).
It then checks the repaired images again and fails if problems remain. It
does not repair anything when `--signer-account` does not match the expected
identity, because the new signatures and attestations would not be accepted.
Repairing needs the same permissions as promotion: write access to the
canonical registry and the ability to get identity tokens for
`--signer-account`.

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

[ggcr-google]: https://pkg.go.dev/github.com/google/go-containerregistry/pkg/v1/google
[issue-1952]: https://github.com/kubernetes-sigs/promo-tools/issues/1952
[k8sio-manifests-dir]: https://git.k8s.io/k8s.io/registry.k8s.io
