# Promotion predicate

Predicate type: `https://k8s.io/promo-tools/promotion/v1`

The image promoter records every promoted digest in an
[in-toto statement](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md)
with this predicate. The statement is signed into a sigstore bundle and
attached to the digest on the canonical registry as an OCI referrer, see
[Provenance generation](./image-promotion.md#provenance-generation).

The predicate is defined in
[`promotion_record.proto`](../promoter/image/provenance/promotion_record.proto)
and serialized with the protobuf JSON mapping, so field names are lower camel
case and empty fields are omitted. New fields are only added, existing fields
keep their meaning.

## Statement

- `subject`: one entry, named by the production reference (for example
  `registry.k8s.io/kube-apiserver`) with the `sha256` digest of the promoted
  manifest or index.
- `predicateType`: `https://k8s.io/promo-tools/promotion/v1`

## Fields

| Field | Type | Description |
|-------|------|-------------|
| `srcRef` | string | Staging reference including the digest, for example `gcr.io/k8s-staging-foo/foo@sha256:…`. Omitted when written by `kpromo sigcheck`. |
| `dstRef` | string | Production reference without digest, for example `registry.k8s.io/foo`. |
| `digest` | string | Promoted digest, for example `sha256:…`. |
| `timestamp` | string | Time of the promotion run (RFC 3339), or of the `kpromo sigcheck` run that wrote the record. All records of a run share it. |
| `builderId` | string | Promoter identity and version, for example `https://k8s.io/promo-tools@v4.6.0`. |
| `tags` | string list | Tags promoted for the digest, sorted. Omitted for digests promoted without a tag. `kpromo sigcheck` records the tags of the digest on the canonical registry. |
| `source` | [ResourceDescriptor](#resourcedescriptor) | Staging image: `name` is the staging repository, `digest` the promoted digest. Omitted when written by `kpromo sigcheck`. |
| `destination` | [ResourceDescriptor](#resourcedescriptor) | Production image: `name` is the production reference, `digest` the promoted digest. |
| `manifest` | [ResourceDescriptor](#resourcedescriptor) | Promoter manifest listing the digest. For thin manifests this is the `images.yaml` file. `name` is the path relative to the repository root, `digest.gitCommit` the checked out commit and `uri` the `git+https` URL of the repository. Outside of a git repository only `name` is set, holding the path as passed to the promoter. Omitted when the digest can't be mapped to a manifest, and when written by `kpromo sigcheck`. |
| `promoter` | [Promoter](#promoter) | Promoter binary. |
| `job` | [Job](#job) | Prow job running the promotion. Omitted when `JOB_NAME` is not set. |

### ResourceDescriptor

The fields used from the in-toto
[ResourceDescriptor](https://github.com/in-toto/attestation/blob/main/spec/v1/resource_descriptor.md),
with the same names and meaning.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Name of the resource. |
| `uri` | string | Location of the resource. |
| `digest` | map | Digests by algorithm, for example `sha256` or `gitCommit`. |

### Promoter

| Field | Type | Description |
|-------|------|-------------|
| `version` | string | Promoter version, for example `v4.6.0`. |
| `gitCommit` | string | Promoter source commit. |

### Job

Values of the environment variables Prow sets for the job.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | `JOB_NAME`, for example `post-k8sio-image-promo`. |
| `type` | string | `JOB_TYPE`, for example `postsubmit` or `periodic`. |
| `buildId` | string | `BUILD_ID`. |
| `prowJobId` | string | `PROW_JOB_ID`. |

## Records written by sigcheck

`kpromo sigcheck --confirm` attests promoted digests that have no promotion
attestation, see
[Checking signatures and attestations](./image-promotion.md#checking-signatures-and-attestations).
It does not know the staging image or the manifest, so its records have no
`srcRef`, `source` and `manifest`, and `job` names the job running
`kpromo sigcheck`.

## Example

```json
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [
    {
      "name": "registry.k8s.io/foo",
      "digest": {"sha256": "709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f"}
    }
  ],
  "predicateType": "https://k8s.io/promo-tools/promotion/v1",
  "predicate": {
    "srcRef": "gcr.io/k8s-staging-foo/foo@sha256:709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f",
    "dstRef": "registry.k8s.io/foo",
    "digest": "sha256:709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f",
    "timestamp": "2026-09-17T07:00:00Z",
    "builderId": "https://k8s.io/promo-tools@v4.6.0",
    "tags": ["v1.0.0"],
    "source": {
      "name": "gcr.io/k8s-staging-foo/foo",
      "digest": {"sha256": "709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f"}
    },
    "destination": {
      "name": "registry.k8s.io/foo",
      "digest": {"sha256": "709e17a9c17018997724ed19afc18dbf576e9af10dfe78c13b34175027916d8f"}
    },
    "manifest": {
      "name": "registry.k8s.io/images/k8s-staging-foo/images.yaml",
      "uri": "git+https://github.com/kubernetes/k8s.io",
      "digest": {"gitCommit": "0123456789abcdef0123456789abcdef01234567"}
    },
    "promoter": {"version": "v4.6.0", "gitCommit": "fedcba9876543210fedcba9876543210fedcba98"},
    "job": {
      "name": "post-k8sio-image-promo",
      "type": "postsubmit",
      "buildId": "1968000000000000000",
      "prowJobId": "0f6d2c4e-0000-0000-0000-000000000000"
    }
  }
}
```

## Changing the predicate

Edit `promotion_record.proto`, run `make update-proto` and update this page.
`make verify-proto` fails when the generated code is out of date.
