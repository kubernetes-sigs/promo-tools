# GitHub Releases to Google Cloud Storage

This directory contains a subcommand called `gh` that downloads a release
published in GitHub to a Google Cloud Storage Bucket.

## Use case

GitHub has a rate limit on downloading release artifacts. The artifacts that are often used (e.g. in CI) are uploaded to Google Cloud Storage, as GCS doesn't have read rate limits. This improves test stability as flakes related to hitting rate limits are avoided.

## Requirements

This subcommand directly depends on `gcloud` and `gsutil` to be installed on
the system.

Google Cloud has [documentation on installing and configuring the Google Cloud SDK CLI tools](https://cloud.google.com/sdk/docs/quickstarts).

## Install

`kpromo gh` is a subcommand of `kpromo` and can be installed via `go install`:

```console
go install sigs.k8s.io/promo-tools/cmd/kpromo
```

This will install `kpromo` to `$(go env GOPATH)/bin/kpromo`.

## Usage

```console
kpromo gh [flags]
```

### Command line flags

```console
$ kpromo gh --help
Uploads GitHub releases to Google Cloud Storage

Usage:
  kpromo gh --org kubernetes --repo release --bucket <bucket> --release-dir <release-dir> [--tags v0.0.0] [--include-prereleases] [--output-dir <temp-dir>] [--download-only] [--config <config-file>] [flags]

Examples:
gh --org kubernetes --repo release --bucket k8s-staging-release-test --release-dir release --tags v0.0.0,v0.0.1

Flags:
      --bucket string         GCS bucket to upload to
      --config string         config file to set all the branch/repositories the user wants to
      --download-only         only download the releases, do not push them to GCS. Requires the output-dir flag to also be set
  -h, --help                  help for gh
      --include-prereleases   specifies whether prerelease assets should be uploaded to GCS
      --org string            GitHub org/user
      --output-dir string     local directory for releases to be downloaded to
      --release-dir string    directory to upload to within the specified GCS bucket
      --repo string           GitHub repo
      --tags strings          release tags to upload to GCS

Global Flags:
      --log-level string   the logging verbosity, either 'panic', 'fatal', 'error', 'warning', 'info', 'debug', 'trace' (default "info")
```

### Example

```console
$ kpromo gh \
  --org kubernetes --repo kubernetes --bucket my-test-bucket \
  --release-dir release --tags v1.22.2
INFO Validating gh2gcs options...
INFO Downloading assets for the following kubernetes/kubernetes release tags: v1.22.2
INFO Download assets for kubernetes/kubernetes@v1.22.2  release=v1.22.2
INFO Writing assets to /var/folders/bz/z3ndv9m504g0pg9wwmj6bds80000gn/T/gh1025503320/kubernetes/kubernetes/v1.22.2  release=v1.22.2
INFO GitHub asset ID: 44868343, download URL: https://github.com/kubernetes/kubernetes/releases/download/v1.22.2/kubernetes.tar.gz
INFO Files downloaded to /var/folders/bz/z3ndv9m504g0pg9wwmj6bds80000gn/T/gh1025503320 directory
INFO Copying /var/folders/bz/z3ndv9m504g0pg9wwmj6bds80000gn/T/gh1025503320/kubernetes/kubernetes/v1.22.2 to GCS (my-test-bucket/release/v1.22.2)
```

## SIG Release managed buckets

The following GCS buckets are managed by SIG Release:

- k8s-artifacts-cni - contains [CNI plugins](https://github.com/containernetworking/plugins) artifacts
- k8s-artifacts-cri-tools - contains [CRI tools](https://github.com/kubernetes-sigs/cri-tools) artifacts (`crictl` and `critest`)

The artifacts are pushed to GCS by
[Release Managers](https://k8s.io/releases/release-managers/). The pushing is
done manually by running the appropriate `kpromo gh` command. It's recommended for
Release Managers to watch the appropriate repositories for new releases.
