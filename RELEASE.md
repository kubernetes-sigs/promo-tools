# Releasing the artifact promoter tools

This is a draft document to describe the release process for artifact promotion
tooling.

(If there are improvements you'd like to see, please comment on the
[tracking issue](https://github.com/kubernetes-sigs/promo-tools/issues/539).)

- [Workflow](#workflow)- [Tracking](#tracking)
- [Tagging](#tagging)
- [Release notes](#release-notes)
- [Image promotion](#image-promotion)
- [Rollout](#rollout)
- [Announce](#announce)

## Tracking

As the first task, a Release Manager should open a tracking issue for the
release.

We don't currently have a template for releasing, but the following
[issue](https://github.com/kubernetes-sigs/promo-tools/issues/523) is a good
example to draw inspiration from.

We're not striving for perfection with the template, but the tracking issue
will serve as a reference point to aggregate feedback, so try your best to be
as descriptive as possible.

## Validation

TODO: Talk about canaries

## Tagging

There are two tags that we care about:

- git tags
- image tags

We use with SemVer-compliant versions for git tags and GitHub releases,
prefixed with `v`.
SemVer is described in detail [here](https://semver.org/).

Example:

```console
v3.4.0
```

Image tags are derived from the git tag, with the addition of a revision.

Example:

```console
v3.4.0-1
```

This is a similar pattern to what you might expect from an OS package.

Including a revision number in the image tags means we have an opportunity to
fix image issues that may be related to infrastructure components.

One example is changing a base image that had unintended consequences.

### Updating version references

Versions are (at the time of writing) described in a few places:

- [VERSION](/VERSION)
- [cloudbuild.yaml](/cloudbuild.yaml)
- [dependencies.yaml](/dependencies.yaml)
- [workspace_status.sh](/workspace_status.sh)

Update `dependencies.yaml` first.

Then run the target for verifying dependencies:

```console
make verify-dependencies
```

This will reveal the other places where dependencies need to be updated.

After references to the tag version have been updated, commit the content and
open a pull request to merge these changes.

Once the pull request has been merged,

## Drafting release notes

## Drafting a GitHub release

## Image promotion

Once the new images are built, they have to be promoted to
make them available on the community production registries. 

To create the image promotion PR follow these instructions:

#### 1. Build `kpromo` from the repository

```
# From the root of your clone of kubernetes-sigs/promo-tools

make kpromo

# The kpromo binary shoold no be in ./bin/kpromo

```

#### 2. Ensure the New Image Is Staged:
```
# Use something like crane to search for v3.4.4

gcrane ls gcr.io/k8s-staging-artifact-promoter/kpromo | grep v3.4.4
v3.4.4-1

# ... or skopeo

skopeo list-tags docker://gcr.io/k8s-staging-artifact-promoter/kpromo | grep v3.4.4
        "v3.4.4-1"
```
#### 3. Create the Image Promotion PR

Before proceeding, make sure you have already a fork of
[kubernetes/k8s.io](https://github.com/kubernetes/k8s.io) in
you github user. You will also need a to export `GITHUB_TOKEN`
with a [personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/creating-a-personal-access-token) 
with enough permissions to create a pull request on your behalf.

Now, run the following from the `promo-tools` repo, replacing 
`yourUsername` with your GitHub userbaname and `v3.4.4-1` with the
actual tag you want to promote:

```
./bin/kpromo pr --fork puerco --interactive --project artifact-promoter --tag v3.4.4-1
```

`kpromo` will ask you some questions before proceeding and it will
open a pull request similar to [k/k8s.io#3933](https://github.com/kubernetes/k8s.io/pull/3933).

#### 4. Check the Image Promotion Process

After merging the PR, the promoter postsubmits will run to promote
and sign you images. To check the status of the PR monitor the
[post-k8sio-image-promo job](https://prow.k8s.io/?job=post-k8sio-image-promo).

## Publishing

## Rollout

## Announce
