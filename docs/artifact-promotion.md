# Promoting files as OCI artifacts

Projects can publish files, like binaries, on `registry.k8s.io` as OCI
artifacts, promoted by the image promoter like container images. A promoted
artifact gets what an image gets: copies in every region, a signature, the
carried staging attestations and a [verification
summary](verification-summaries.md). [File promotion](file-promotion.md)
copies files between GCS buckets without any of these.

Following the layout below keeps plain downloads working: a file can be
downloaded with `curl` by its sha256, without any OCI tooling.

- [Who should use this](#who-should-use-this)
- [The layout](#the-layout)
- [Building artifacts with oras](#building-artifacts-with-oras)
- [Attesting and promoting](#attesting-and-promoting)
- [Downloading](#downloading)
- [Moving from file promotion](#moving-from-file-promotion)

## Who should use this

The project pays for the traffic of `registry.k8s.io`, while GitHub Releases
serves files for free, so GitHub Releases stays the default for binaries.
This is for projects that promote files already, see [moving from file
promotion](#moving-from-file-promotion), where it moves traffic that the
project serves today. Other projects need the agreement of SIG K8s Infra
first.

## The layout

- **One repository per file, one tag per version**, for example
  `registry.k8s.io/<project>/<file>:v1.2.3`.
- **One manifest per file and platform.** A file built for several
  platforms is an image index with one manifest per platform, and the
  platform is only set on the descriptors of the index. A file that is the
  same on every platform is a single manifest.
- **An empty config and a versioned artifact type.** The config is the empty
  descriptor (`application/vnd.oci.empty.v1+json`). The manifests and the
  index set `artifactType` to the kind of file and its layout version, for
  example `application/vnd.k8s.<project>.<file>.v1`.
- **The file as the only layer, byte for byte.** The layer is the file
  itself with the media type `application/octet-stream`, not a tar archive
  and not compressed, so its digest is the sha256 of the file.
- **The file name and its sha512 as layer annotations.**
  `org.opencontainers.image.title` holds the file name, which tools like
  `oras pull` write the file as. The sha512, for consumers that verify
  files by sha512, goes into an annotation of the project, for example
  `io.k8s.<project>.sha512`.
- **Attestations as OCI referrers** of each manifest digest, like for
  images. The promoter adds its signature as a cosign
  `sha256-<digest>.sig` tag and its promotion record and summary as
  referrers, see [what a promoted image
  carries](verification-summaries.md#what-a-promoted-image-carries).
- **Image manifests**, not the deprecated
  `application/vnd.oci.artifact.manifest.v1+json`, which the promoter
  rejects, see [OCI artifacts](image-promotion.md#oci-artifacts).

The `spoc` binary of the Security Profiles Operator follows this layout.
`registry.k8s.io/security-profiles-operator/spoc:v1.1.1` is an index with
one manifest per platform, and the `linux/amd64` one is:

```json
{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "artifactType": "application/vnd.k8s.security-profiles-operator.spoc.v1",
  "config": {
    "mediaType": "application/vnd.oci.empty.v1+json",
    "digest": "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
    "size": 2
  },
  "layers": [
    {
      "mediaType": "application/octet-stream",
      "digest": "sha256:8f06a2f5500bafb684a54c45b9c65b1d7911633d9292aca66b33feac1eafad6c",
      "size": 78167432,
      "annotations": {
        "org.opencontainers.image.title": "spoc",
        "io.k8s.security-profiles-operator.sha512": "e9de5959…"
      }
    }
  ],
  "annotations": {
    "org.opencontainers.image.created": "2026-10-05T08:26:33Z",
    "org.opencontainers.image.revision": "530ad36f753f370091100cc0fb7ebeb24d4199f7",
    "org.opencontainers.image.source": "https://github.com/kubernetes-sigs/security-profiles-operator",
    "org.opencontainers.image.version": "v1.1.1"
  }
}
```

The manifest annotations are optional. A fixed `created`, like the commit
time here, keeps the digest reproducible, see below.

## Building artifacts with oras

[oras](https://oras.land) builds this layout, except for the platform:
oras keeps the platform in the config. Its `--artifact-platform` flag
replaces the empty config with an image config that holds the platform,
and `oras manifest index create` reads the platform of each manifest from
its config, so with the empty config the index gets none. So push the file
of each platform without `--artifact-platform`, and write the index
yourself. With oras v1.3, for a file `kubectl-foo` of each platform in a
directory named after the platform, in a git checkout of its source:

```shell
REPO=us-central1-docker.pkg.dev/k8s-staging-images/<project>/kubectl-foo
VERSION=v1.2.3
TYPE=application/vnd.k8s.<project>.kubectl-foo.v1
CREATED=$(TZ=UTC0 git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ)

for PLATFORM in linux/amd64 linux/arm64; do
  (
    cd "$PLATFORM"
    jq -n --arg sha512 "$(sha512sum kubectl-foo | cut -d' ' -f1)" --arg created "$CREATED" '{
      "kubectl-foo": {"io.k8s.<project>.sha512": $sha512},
      "$manifest": {"org.opencontainers.image.created": $created}
    }' > annotations.json
    oras push --artifact-type "$TYPE" --annotation-file annotations.json \
      "$REPO:$VERSION-${PLATFORM//\//-}" kubectl-foo:application/octet-stream
  )
done

jq -n --arg type "$TYPE" \
  --argjson amd64 "$(oras manifest fetch --descriptor "$REPO:$VERSION-linux-amd64")" \
  --argjson arm64 "$(oras manifest fetch --descriptor "$REPO:$VERSION-linux-arm64")" '{
    schemaVersion: 2,
    mediaType: "application/vnd.oci.image.index.v1+json",
    artifactType: $type,
    manifests: [
      $amd64 + {artifactType: $type, platform: {os: "linux", architecture: "amd64"}},
      $arm64 + {artifactType: $type, platform: {os: "linux", architecture: "arm64"}}
    ]
  }' > index.json
oras manifest push --media-type application/vnd.oci.image.index.v1+json "$REPO:$VERSION" index.json
```

- oras uses the credentials of `docker login` and Docker credential
  helpers, for Artifact Registry for example `gcloud auth
  configure-docker us-central1-docker.pkg.dev`.
- Use oras v1.1 or later: older versions write the config as
  `application/vnd.unknown.config.v1+json` instead of the empty descriptor
  of the layout.
- Push the file, not a directory: oras packs directories into a tar
  archive.
- oras sets `org.opencontainers.image.created` to the time of the push,
  which gives every push another digest. The commit time instead makes the
  digests reproducible, which the GitHub Actions route for SLSA build
  level 3 relies on, see below.
- The per-platform tags are for staging only. Promote the index, its
  manifests come with it.

## Attesting and promoting

Artifacts are attested and promoted like images:

1. Attest the SLSA build provenance of each manifest in staging: of each
   platform manifest of an index, or of the single manifest of a file
   without platforms. See [getting summaries with a build
   level](verification-summaries.md#getting-summaries-with-a-build-level).
   An index needs no provenance of its own: it gets the lowest level of
   its platform manifests, and a manifest without provenance gets no
   level. Attest before you promote, since a digest keeps the summary of
   its first promotion.
   The staging build on Prow and Cloud Build signs its provenance itself,
   which reaches SLSA build level 1, enough for the verification summaries.
   Level 3 needs provenance that the build can't forge, which only GitHub's
   isolated provenance workflow provides in the project today: build the
   artifacts on GitHub Actions, attest them there, and push them byte for
   byte to staging from Prow, like the Security Profiles Operator does, see
   its [release
   documentation](https://github.com/kubernetes-sigs/security-profiles-operator/blob/main/doc/release.md#oci-artifacts).
   Cloud Build's own provenance may become a route to level 3 without GitHub
   Actions, see
   [kubernetes/release#2616](https://github.com/kubernetes/release/issues/2616).
2. Promote the digest of the index, or of the single manifest, with its
   version tag in the `images.yaml` of your project in
   [kubernetes/k8s.io](https://github.com/kubernetes/k8s.io/tree/main/registry.k8s.io/images),
   like an image:

   ```yaml
   - name: spoc
     dmap:
       "sha256:5ef0ab5aee2157251534834fd1e124d3866df83674bf12b33244cd6ceb5dc9ad": ["v1.1.1"]
   ```

The provenance policy of your project, and with it the verification
summary, applies to artifacts as to images.

## Downloading

With oras, which writes the file under its title:

```shell
oras pull --platform linux/amd64 registry.k8s.io/security-profiles-operator/spoc:v1.1.1
```

With `curl` and the sha256 of the file, which is the digest of its layer.
`curl` doesn't check the content, so check it after the download:

```shell
DIGEST=sha256:8f06a2f5500bafb684a54c45b9c65b1d7911633d9292aca66b33feac1eafad6c
curl -fsSLo spoc "https://registry.k8s.io/v2/security-profiles-operator/spoc/blobs/$DIGEST"
echo "${DIGEST#sha256:}  spoc" | sha256sum --check
```

Projects that already publish the sha256 of their files can download them
like this. To look up the digest of a version, read the layer of the
platform manifest, for example with
[`crane`](https://github.com/google/go-containerregistry/tree/main/cmd/crane):

```shell
crane manifest --platform linux/amd64 registry.k8s.io/security-profiles-operator/spoc:v1.1.1 |
  jq -r '.layers[0].digest'
```

Verify the signature and the verification summary of the platform manifest
or the index as for images, see [verifying a
summary](verification-summaries.md#verifying-a-summary). They are about the
manifest, and its layer digest ties the file to it: a file downloaded with
`curl` is the one the summary covers when its sha256 is the layer digest of
the verified manifest.

## Moving from file promotion

To move a project from file promotion, build the artifacts in staging next
to the files, promote both for a few releases, and switch the consumers to
`registry.k8s.io` before the file promotion of the project stops. The
remaining users of file promotion and its deprecation are tracked in
[#1960](https://github.com/kubernetes-sigs/promo-tools/issues/1960).
