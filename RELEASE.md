# Releasing the artifact promoter tools

This is a draft document to describe the release process for artifact promotion
tooling.

(If there are improvements you'd like to see, please comment on the
[tracking issue](https://github.com/kubernetes-sigs/promo-tools/issues/539).)

- [Tracking](#tracking)
- [Tagging](#tagging)
  - [Creating the release](#creating-the-release)
- [Image promotion](#image-promotion)
- [Rollout](#rollout)
- [Announce](#announce)

## Tracking

As the first task, a Release Manager should open a tracking issue for the
release with the
[cut release template](https://github.com/kubernetes-sigs/promo-tools/issues/new?template=cut-release.md).

The tracking issue will serve as a reference point to aggregate feedback, so
try your best to be as descriptive as possible.

## Tagging

There are two tags that we care about:

- git tags
- image tags

We use with SemVer-compliant versions for git tags and GitHub releases,
prefixed with `v`.
SemVer is described in detail [here](https://semver.org/).

Example:

```console
vX.Y.Z
```

Image tags are derived from the git tag, with the addition of a revision.

Example:

```console
vX.Y.Z-0
```

The revision is always `0` for images built from a release tag. To fix an
image issue that is not related to the code, such as a base image change,
cut a new patch release.

### Creating the release

There are no version references to update in the repository. Tag the commit
on `main` to release with a signed tag, push it and create the GitHub release
for it, for example:

```console
git tag -s -m vX.Y.Z vX.Y.Z <commit>
git push https://github.com/kubernetes-sigs/promo-tools vX.Y.Z
gh release create vX.Y.Z --repo kubernetes-sigs/promo-tools --verify-tag --generate-notes
```

Pushing the tag runs the `post-kpromo-push-image` job, which builds the image
and pushes it to staging with the tags `vYYYYMMDD-<git describe>` and
`vX.Y.Z-0`. Builds of `main` push `vYYYYMMDD-<git describe>` and `latest`, so
`latest` stays on `main` when an older commit gets tagged.

To find the image build run of the tag, open the
[recent runs of the job](https://prow.k8s.io/?job=post-kpromo-push-image) and
pick the one whose ref is the tag (`vX.Y.Z`). Prow only lists the runs of the
last few days, older runs are in the
[job history](https://prow.k8s.io/job-history/gs/kubernetes-ci-logs/logs/post-kpromo-push-image).
Link the run in the release tracking issue.

## Image promotion

Once the new images are built, they have to be promoted to
make them available on the community production registries. 

To create the image promotion PR follow these instructions:

#### 1. Build `kpromo` from the repository

```
# From the root of your clone of kubernetes-sigs/promo-tools

make kpromo

# The kpromo binary should now be in ./bin/kpromo

```

#### 2. Ensure the New Image Is Staged:
```
# Use something like crane to search for vX.Y.Z

gcrane ls gcr.io/k8s-staging-artifact-promoter/kpromo | grep vX.Y.Z
vX.Y.Z-0

# ... or skopeo

skopeo list-tags docker://gcr.io/k8s-staging-artifact-promoter/kpromo | grep vX.Y.Z
        "vX.Y.Z-0"
```
#### 3. Create the Image Promotion PR

Before proceeding, make sure you have already a fork of
[kubernetes/k8s.io](https://github.com/kubernetes/k8s.io) in
you github user. You will also need a to export `GITHUB_TOKEN`
with a [personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/creating-a-personal-access-token) 
with enough permissions to create a pull request on your behalf.

Now, run the following from the `promo-tools` repo, replacing 
`yourUsername` with your GitHub username and the version with the
actual tag you want to promote:

```
./bin/kpromo pr --fork yourUsername --interactive --project artifact-promoter --tag vX.Y.Z-0
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
