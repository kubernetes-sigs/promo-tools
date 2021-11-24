# Artifact Promotion Tooling

[![PkgGoDev](https://pkg.go.dev/badge/sigs.k8s.io/promo-tools/v3)](https://pkg.go.dev/sigs.k8s.io/promo-tools/v3)
[![Go Report Card](https://goreportcard.com/badge/sigs.k8s.io/promo-tools/v3)](https://goreportcard.com/report/sigs.k8s.io/promo-tools/v3)
[![Slack](https://img.shields.io/badge/Slack-%23release--management-blueviolet)](https://kubernetes.slack.com/archives/C2C40FMNF)

This repository contains a suite of tools responsible for artifact promotion
in the Kubernetes project.

## DISCLAIMER

Before getting started, it's important to note that the tooling stored here
is an aggregate of several efforts to improve artifact promotion, across
multiple SIGs, subprojects, and contributors.

We call that out to set the expectation that:

- you may see duplicated code within the codebase
- you may encounter multiple techniques/tools for accomplishing the same thing
- you will encounter several TODOs
- you will see gaps in documentation including:
  - missing documentation
  - example commands that may not work
  - broken links

This list is far from exhaustive.

If you encounter issues, please search for existing issues/PRs in the
repository and join the conversation.

If you cannot find an existing issue, please file a detailed report, so that
maintainers can work on it.

- [DISCLAIMER](#disclaimer)
- [`kpromo`](#kpromo)
  - [Installation](#installation)
    - [User](#user)
    - [Developer](#developer)
  - [Usage](#usage)
  - [Image promotion](#image-promotion)
  - [File promotion](#file-promotion)
  - [GitHub promotion](#github-promotion)

## `kpromo`

`kpromo`, or the **K**ubernetes **Promo**tion Tool, is the canonical tool
for promoting Kubernetes project artifacts.

It wraps and unifies the functionality of multiple tools that have existed in
the past:

- `cip`
- `cip-mm`
- `cip-auditor`
- `gh2gcs`
- `krel promote-images`
- `promobot-files`

### Installation

Requirements:

- [Docker][docker]
- [Go][golang]

#### User

If you're interested in installing `kpromo` from a tag:

```console
go install sigs.k8s.io/promo-tools/v3/cmd/kpromo@<tag>
$(go env GOPATH)/bin/kpromo <subcommand>
```

#### Developer

If you're interested in actively contributing to `kpromo` or testing
functionality which may not yet be in a tagged release, first fork/clone the
repo and then run:

```console
go install ./cmd/kpromo/...
$(go env GOPATH)/bin/kpromo <subcommand>
```

### Usage

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

### Image promotion

For background on the image promotion process, see
[here](/docs/image-promotion.md).

To create an image promotion PR via `kpromo pr`, see
[here](docs/promotion-pull-requests.md).

### File promotion

See [here](/docs/file-promotion.md).

### GitHub promotion

See [here](/docs/github-promotion.md).

[docker]: https://docs.docker.com/get-docker
[golang]: https://golang.org/doc/install
