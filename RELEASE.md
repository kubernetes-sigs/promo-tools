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

## Publishing

## Rollout

## Announce
