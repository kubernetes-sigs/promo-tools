# Container Image Promoter

The Container Image Promoter (aka "CIP") promotes Docker images from one
Registry (src registry) to another (dest registry). The set of images to promote
are defined by promoter manifests, in YAML.

Currently only Google Container Registry (GCR) is supported.

# kpromo - Artifact promoter

kpromo is a tool responsible for artifact promotion.

It has two operation modes:

- `run` - Execute a file promotion (formerly "promobot-files") (image promotion coming soon)
- `manifest` - Generate/modify a file manifest to target for promotion (image support coming soon)

Expectations:

- `kpromo run` should only be run in auditable environments
- `kpromo manifest` should primarily be run by contributors

- [Usage](#usage)
- [Image promotion](#image-promotion)
- [File promotion](#file-promotion)
- [GitHub promotion](#github-promotion)

## Usage

```console
Usage:
  kpromo [command]

Available Commands:
  cip         Promote images from a staging registry to production
  completion  generate the autocompletion script for the specified shell
  gh          Uploads GitHub releases to Google Cloud Storage
  help        Help about any command
  manifest    Generate/modify a manifest for artifact promotion
  pr          Starts an image promotion for a given image tag
  run         Run artifact promotion
  version     output version information
```

## Image promotion

See [here](./image-promotion.md).

## File promotion

See [here](./file-promotion.md).

## GitHub promotion

See [here](./github-promotion.md).

[docker]: https://docs.docker.com/get-docker
[golang]: https://golang.org/doc/install
