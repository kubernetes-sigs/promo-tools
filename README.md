# Container Image Promoter

The Container Image Promoter (aka "cip") promotes images from one Docker
Registry (src registry) to another (dest registry), by reading a Manifest file
(in YAML). The Manifest lists Docker images, and all such images are considered
"blessed" and will be copied from src to dest.

Example Manifest:

```
registries:
  src: gcr.io/myproject-staging-area
  dest: gcr.io/myproject-production
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

Here, the Manifest cares about 3 images --- `apple`, `banana`, and `cherry`. The
promoter will scan `gcr.io/staging-area` (*src*) as well as `gcr.io/production`
(*dest*). If any of the images are missing from *dest*, then they will be copied
over from *src*.

Currently only Google Container Registry (GCR) is supported.

# Install

```
cip_path=$(go env GOPATH)/src/github.com/GoogleCloudPlatform/k8s-container-image-promoter
git clone https://github.com/GoogleCloudPlatform/k8s-container-image-promoter \
    $cip_path
cd $cip_path
dep ensure
make build
```

# Running the Promoter

The promoter relies on calls to `gcloud container images ...` to realize the
intent of the Manifest. It also tries to run the command as the account in
`service-account`. The credentials for this service account must already be set
up in the environment prior to running the promoter.

Given the Example Manifest as above, you can run the promoter with:

```
cip -verbosity=3 -manifest=path/to/manifest.yaml
```
