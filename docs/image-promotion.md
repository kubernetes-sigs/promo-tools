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
  - [OCI artifacts](#oci-artifacts)
- [Signing and attestation](#signing-and-attestation)
- [Provenance verification](#provenance-verification)
  - [Provenance policies](#provenance-policies)
  - [Carrying staging attestations](#carrying-staging-attestations)
  - [Attestation discovery](#attestation-discovery)
- [Provenance generation](#provenance-generation)
  - [Verification summaries](#verification-summaries)
  - [Repairing carried attestations and summaries](#repairing-carried-attestations-and-summaries)
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
| 2 | **plan** | Parse manifests, read registry inventories, compute promotion edges, reject [unsupported artifacts](#oci-artifacts) |
| 3 | **provenance** | Log all attestations of the staging images ([Attestation discovery](#attestation-discovery)), then verify build-time provenance attestations (verify-if-present, or the project's [provenance policy](#provenance-policies), see [Provenance verification](#provenance-verification)) |
| 4 | **validate** | Validate staging image signatures |
| 5 | **promote** | Copy images from staging to production |
| 6 | **sign** | Sign promoted images with cosign (primary registry only) |
| 7 | **attest** | Generate promotion provenance attestations, [carry staging attestations](#carrying-staging-attestations) and, with `--verification-summaries`, write [verification summaries](#verification-summaries) |
| 8 | **repair** | [Carry the attestations and write the summaries](#repairing-carried-attestations-and-summaries) that promoted images with a provenance policy miss |

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

### OCI artifacts

Besides container images, the promoter copies OCI artifacts that use the
image manifest, like Helm charts or seccomp profiles with a custom config
media type or `artifactType`, and indexes of them. Other shapes can't be
signed recursively, for example the deprecated
`application/vnd.oci.artifact.manifest.v1+json` media type or an index child
that is neither an image manifest nor an index. The plan phase rejects them
before anything is copied, with an error naming the image, the offending
digest and its media type.

## Signing and attestation

After promotion, images are signed using [cosign](https://github.com/sigstore/cosign)
with a keyless (OIDC) identity. For destinations under the Kubernetes
production path (`k8s-artifacts-prod/images`), signatures use the production
(`registry.k8s.io`) reference of the image as their subject. When the
canonical registry (`us-central1-docker.pkg.dev`) is among the promotion
candidates, signatures are pushed there and served globally through
registry.k8s.io via the `SIGNATURE_UPSTREAM_ENDPOINT` routing in archeio.
The signing identity is configured with `--signer-account`.
A digest that already has a signature of that identity for its production
reference on the canonical registry, for example because it was promoted
before under another tag, is not signed again, and the staging signatures
are not copied for it.

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
- `--summary-signer-account` — service account identity for signing the
  [verification summaries](#verification-summaries) (default:
  `--signer-account`)
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
key still blocks promotion, unless the project declares a provenance policy.

### Provenance policies

A project can declare what its staging images must be attested with in the
`provenance` section of its promoter manifest. The images promoted from its
source registry, including nested repositories that have no manifest of
their own, are then checked against that policy:

```yaml
registries:
- name: us-central1-docker.pkg.dev/k8s-staging-images/sp-operator
  src: true
- name: us-central1-docker.pkg.dev/k8s-artifacts-prod/images/security-profiles-operator
provenance:
  mode: require
  signers:
  - sigstore::https://accounts.google.com::sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com
  builders:
  - id: https://cloudbuild.googleapis.com/projects/k8s-staging-images/serviceAccounts/sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com/cloudbuild.yaml
    level: 1
  sources:
  - github.com/kubernetes-sigs/security-profiles-operator
  predicateTypes:
  - https://spdx.dev/Document
```

- `mode`: `off` (the default) keeps the verify-if-present check, `warn` logs
  every violation and promotes anyway, and `require` blocks the promotion.
  Only `require` replaces the verify-if-present check, so trying a policy in
  `warn` mode never weakens the promotion.
- `signers`: the identities trusted to sign the staging attestations, as
  [signer identity specs][signer-principals], for example
  `sigstore::<issuer>::<identity>`, or
  `sigstore(identityMatch=regex)::<issuer>::<identity regexp>` to match
  several. A regexp has to match the whole identity. Only sigstore
  identities with both an issuer and an identity are accepted. They are
  separate from the identity the promoter signs with.
- `builders`: the trusted builders of the SLSA build provenance, each with
  its `id` and the SLSA build `level` it reaches, 1 to 3. An ID without `@`
  also matches the builder at any ref. The level depends on how the builder
  isolates builds and who generates and signs the provenance, which the
  provenance itself can't show: a build that generates and signs its own
  provenance, like the example, reaches level 1 only, because its build
  steps could forge it. The builder ID is what the provenance claims, so
  provenance verifies at no more than the lowest level of the builders its
  signer may claim. A builder can name the policy `signers` that may claim
  it, as written there. A signer that builders name may claim only the
  builders that name it, and the other signers only the builders that name
  no signers, so an isolated provenance generator can reach level 3 next
  to a self-signed build at level 1:

  ```yaml
  signers:
  - sigstore::https://accounts.google.com::sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com
  - sigstore(identityMatch=regex)::https://token.actions.githubusercontent.com::https://github\.com/kubernetes-sigs/security-profiles-operator/\.github/workflows/provenance\.yml@refs/tags/v[0-9.]+
  builders:
  - id: https://cloudbuild.googleapis.com/projects/k8s-staging-images/serviceAccounts/sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com/cloudbuild.yaml
    level: 1
  - id: https://github.com/kubernetes-sigs/security-profiles-operator/.github/workflows/provenance.yml
    level: 3
    signers:
    - sigstore(identityMatch=regex)::https://token.actions.githubusercontent.com::https://github\.com/kubernetes-sigs/security-profiles-operator/\.github/workflows/provenance\.yml@refs/tags/v[0-9.]+
  ```

  GitHub Actions provenance names the signing workflow as its builder,
  and the [SLSA verifier][slsa-verifier] checks that it does. A signer
  identity that matches several `signers` may claim the builders that name
  any of them, at no more than their lowest level. Every builder ID may be
  listed once, and a builder that names signers and one that doesn't must
  not match each other through an ID without `@`.
- `sources`: the repositories the images may be built from, without a ref.
- `predicateTypes` (optional): predicate types that must also be attested
  for every image by one of the signers, for example an SBOM.
- `level` (optional): the SLSA build level the provenance must reach in the
  [SLSA verifier][slsa-verifier]'s controls, 2 or 3, and at most the
  highest level provenance can verify at with the builders of one signer.
  By default every applicable SLSA build control has to pass. Lower levels
  are not accepted, because they would not enforce the builders. The
  verified level is the lower of the level the provenance reaches and the
  lowest level of the builders its signer may claim, and provenance below
  `level` doesn't count. When several build provenances of an image pass,
  for example of a self-signed build and of an isolated provenance
  generator, the image gets the highest of their levels. The validation
  can't tell which `signers` an identity matches, so provenance of an
  identity that matches several may stay below `level`.

`signers`, `builders` and `sources` are required unless the mode is `off`.

An image satisfies the policy when it carries SLSA build provenance (v0.2
or v1) that verifies with the [SLSA verifier][slsa-verifier]: its
signature verified, it was signed by one of the signers, its subjects
include the image digest, and it names one of the builders and one of the
sources. Unsigned attestations, attestations whose signature does not
verify, attestations by other signers and attestations about other digests
never count. If discovering the attestations fails, the policy is not
satisfied either.

For an index, each of its platform manifests in the same repository is
evaluated the same way, against its own digest and attestations, because
builders usually attest the platform images rather than the index. The
index satisfies the policy with provenance about its own digest or, when it
has no build provenance of its own, when all its platform manifests satisfy
it; the verified level is then the lowest of theirs. Provenance about the
index that fails the policy fails the index, whatever its platform
manifests carry. A nested index is evaluated against its own attestations
only. Attestation manifests BuildKit adds to an index (annotated and with
the platform `unknown/unknown`) are no platform manifests. Platform images
a build attested in their own
repositories (for example `…-amd64`) satisfy the policy of those
repositories when they are promoted on their own.

Every run logs the effective policy per source image, so dry runs show
which images are checked against which policy. Manifests that share a
source registry must declare the same policy. The policy of a source
registry also applies to the repositories below it, so an image has to
satisfy the policies of every manifest whose source registry contains it,
whichever manifest promotes it. A policy without a `mode` is off, which is
logged as a warning when it declares anything else.

### Carrying staging attestations

When an image is promoted, the staging attestations its provenance policies
accepted are copied to the canonical registry: attestations whose signature
verified, that are about the image digest, that one of the policy signers
signed and, for build provenance, that passed the policy. Every enabled
policy that applies to the image has to be satisfied and accept an
attestation. The attestations are OCI referrers of the image digest, which
promotion keeps, so they are copied digest-identical and stay verifiable as
they are, next to the promotion record, and are served through
registry.k8s.io like the signatures.

Only referrers that hold exactly the accepted sigstore bundle, with the
bundle artifact type, the empty config, the image digest as subject and the
predicate type of the accepted attestation, are copied, so that no
unverified content reaches production. Carrying is idempotent and, like the
promotion records, needs signing. Images without an enabled policy carry
nothing, attestations in legacy `.att` tags are not carried, and neither are
promotion records or verification summaries, which the promoter writes
itself. The attestations of platform manifests that the promoter manifest
doesn't list are not carried either, even when their index satisfied the
policy through them. A failure to carry is reported without stopping the others,
and the [repair phase](#repairing-carried-attestations-and-summaries) of a
later run that parses the digest retries it.

### Attestation discovery

After the verification, the promoter lists every attestation of each staging
image and logs it, dry runs included:

- sigstore bundles attached as OCI referrers, as cosign v3 writes them for
  signatures and attestations
- DSSE envelopes and unsigned in-toto statements in legacy cosign `.att` tags

For an index, the referrers and `.att` tags of its direct children in the same
repository are listed too, except for the attestation manifests BuildKit
adds. Referrers are per repository, so attestations that
a build attached to the platform images in their own repositories (for
example `…-amd64`) are found when those repositories are promoted.

Each attestation is verified cryptographically against the sigstore trust
roots of [carabiner-dev/signer][signer] (the public good instance and
GitHub's), without checking who signed it, and logged with its predicate
type, location, signature status (`verified`, `unsigned`, `unverifiable` or
`failed`) and the verified signers as
[signer principals][signer-principals]. A verified attestation can be copied
onto another image, so the log also says when a statement is not about the
digest it is attached to:

```text
Attestations found for us-central1-docker.pkg.dev/k8s-staging-images/sp-operator/security-profiles-operator-amd64@sha256:d2a1…:
  sha256:d2a1… referrer sha256:64e1…: https://slsa.dev/provenance/v1 (verified by sigstore::https://accounts.google.com::sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com)
  sha256:d2a1… referrer sha256:08f1…: https://sigstore.dev/cosign/sign/v1 (verified by sigstore::https://accounts.google.com::sp-operator-sa@k8s-staging-images.iam.gserviceaccount.com)
```

Referrers without an attestation are listed with their artifact type, and
referrers or statement layers that can't be read are logged as skipped,
without hiding the attestations next to them. DSSE layers of an `.att` tag
are read together: if one of them can't be read, the whole tag is logged as
skipped, and layers whose payload is not an in-toto statement are left out.
Without a [provenance policy](#provenance-policies), discovery does not
affect promotion: a failed discovery is logged as a warning, which tells it
apart from an image without attestations. With a policy, the discovered
attestations are what the policy is evaluated against.

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

### Verification summaries

With `--verification-summaries` (off by default), the promoter also writes a
signed [SLSA verification summary][slsa-vsa] (VSA) for each promoted digest,
index and platform manifests alike, next to the promotion record: an in-toto
statement with the `https://slsa.dev/verification_summary/v1` predicate
type, signed into a sigstore bundle and attached to the digest on the
canonical registry through the OCI referrers API. A digest keeps the summary
of its first promotion; summaries of other verifiers, for example one a
build attached in staging, don't count. The summary is made of what the
[provenance verification](#provenance-verification) found for the staging
images the digest was promoted from, usually one:

- `verifier.id` is `https://k8s.io/promo-tools/verifier/v1`, and
  `verifier.version.kpromo` the promoter version.
- `resourceUri` is the `registry.k8s.io` reference of the digest, which is
  also the subject.
- `policy` is the promoter manifest the image was promoted from: its
  repository and path as `uri` (for example
  `git+https://github.com/kubernetes/k8s.io#registry.k8s.io/manifests/<project>/promoter-manifest.yaml`),
  and the commit it was read from as `digest.gitCommit`. When the policies
  that applied come from other manifests too, for example of a parent source
  registry, the `uri` is the repository only. No summary is written for a
  manifest that is not at a commit of a repository.
- `verificationResult` is `PASSED` when every
  [provenance policy](#provenance-policies) that applies to the staging
  images is satisfied, and `FAILED` otherwise. That happens in `warn` mode,
  because `require` blocks the promotion, and for images that the
  [repair phase](#repairing-carried-attestations-and-summaries) summarizes
  after their promotion. A failed summary has the verified level `FAILED`.
- `verifiedLevels` of a passed summary are the lowest SLSA build level the
  policies verified (`SLSA_BUILD_LEVEL_<n>`), or
  `SLSA_BUILD_LEVEL_UNEVALUATED` when a staging image had no policy or a
  policy verified no level, plus `K8S_PROMOTION_MANIFEST_REVIEWED`.
- `inputAttestations` are the attestations the policies accepted: their
  location in the staging repository and the digest of their sigstore
  bundle, or, for legacy `.att` tags, of their payload. Carried attestations
  keep these digests in the production registry.
- `slsaVersion` is `1.0`, the SLSA version whose build track the policies
  are evaluated against.

The summary of an index covers the index itself; when the index satisfied
the policies through its platform manifests, its input attestations are
theirs. The platform manifests get summaries of their own: from their own
policy results when the promoter manifest lists them, and otherwise from
the policy results of their own attestations when these satisfy the
policies, whether or not the index satisfied them on its own. Any other
platform manifest, because no policy applies or its own attestations don't
satisfy the policies, gets a summary that claims no build level
(`SLSA_BUILD_LEVEL_UNEVALUATED`) and no input attestations, and that fails
when the summary of one of the indexes holding it fails.
Attestation manifests BuildKit adds to an index get none.

The summaries are signed by the identity of `--summary-signer-account`, or of
`--signer-account`, which signs the images and promotion records, when it is
not set. A dedicated identity that only the production promotion jobs can use
is meant to sign them ([#1955][issue-1955]); until it is set up, they are off
by default. A summary counts as written only when it is signed by that
identity, so after the identity changes, every digest whose summary is
written again, for example because it is promoted under a new tag, gets one
of the new identity. The identity is meant to be set before the summaries
are turned on. Consumers pin both the verifier and its signer, with `$SIGNER` the
identity of `--summary-signer-account` in the production promotion jobs, for
example with the [SLSA verifier][slsa-verifier]:

```console
slsa-verifier vsa \
  --verifier "https://k8s.io/promo-tools/verifier/v1=sigstore::https://accounts.google.com::$SIGNER" \
  --level SLSA_BUILD_LEVEL_3 vsa.sigstore.json
```

or with `cosign verify-attestation --new-bundle-format --type
https://slsa.dev/verification_summary/v1` and the signer's identity and
issuer.

### Repairing carried attestations and summaries

Promoted images are no promotion candidates in later runs, so a failed attest
phase would leave them without their carried attestations or verification
summaries for good, and `kpromo sigcheck` has no promoter manifests to repair
them. The repair phase checks the images of the parsed manifests with an
enabled [provenance policy](#provenance-policies) that the run doesn't
promote. It compares the attestation referrers of the staging image with
those in the canonical registry, and, with `--verification-summaries`, looks
for the promoter's summary of the digest and of the platform manifests of an
index that the promoter manifest doesn't list. That takes about six requests
per digest and three more per platform manifest of an index, and nothing for
manifests without a policy.

Images that miss something are discovered and evaluated against their
policies again, then carried and summarized as in the attest phase. They are
promoted already, so a policy they no longer satisfy blocks nothing and their
summary records it as failed. Images whose attestations can't be discovered,
for example because they are gone from staging, get nothing written. Images
that don't satisfy their policies, or have a staging attestation the policies
don't accept, never get it carried. All of them are discovered and evaluated
again in every run that parses them. Promotion records are repaired by
`kpromo sigcheck`. Repair failures are logged as warnings and don't fail the
run, which promoted its images already, and later runs retry them.

A run only sees the digests its manifests are parsed with. With
`--use-prow-manifest-diff` that is the digests the change added, and with
`--manifest-diff-since` the digests added in that time. The production jobs,
a postsubmit with the former and a daily periodic with
`--manifest-diff-since=14 days`, therefore repair a failed attest phase for 14
days. Older images, for example when a project adds a policy or the summaries
are turned on, are only filled in by a run without these flags. The repair
phase needs signing and, like promotion, `--confirm`.

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
the scan to fail (0=UNSPECIFIED through 5=CRITICAL). Artifacts without a
container image are not applicable and are not scanned. See
[checks](./checks.md) for details.

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
[signer]: https://github.com/carabiner-dev/signer
[k8sio-manifests-dir]: https://git.k8s.io/k8s.io/registry.k8s.io
[signer-principals]: https://github.com/carabiner-dev/signer/blob/main/docs/principals.md
[slsa-verifier]: https://github.com/slsa-framework/verifier
[issue-1955]: https://github.com/kubernetes-sigs/promo-tools/issues/1955
[slsa-vsa]: https://slsa.dev/spec/v1.0/verification_summary
