# Container Image Promoter

The Container Image Promoter (aka "CIP") promotes Docker images from one
Registry (src registry) to another (dest registry). The set of images to promote
are defined by promoter manifests, in YAML.

Currently only Google Container Registry (GCR) is supported.

- [Install](#install)

## Install

1. Install [Docker][docker] & [Go][golang].
2. Run the steps below:

```console
go get sigs.k8s.io/promo-tools
cd $GOPATH/src/sigs.k8s.io/promo-tools

# Install the "cip" binary into $GOPATH/bin
make install
```

[docker]: https://docs.docker.com/get-docker
[golang]: https://golang.org/doc/install
