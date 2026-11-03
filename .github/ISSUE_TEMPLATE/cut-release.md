---
name: Cut a new promo-tools release
about: Create a tracking issue for a promo-tools release cut
title: Release k-sigs/promo-tools@vX.Y.Z
labels: sig/release, area/release-eng

---

### vX.Y.Z

Issue track for the upcoming release of promo-tools X.Y.Z.

<!-- Example: https://github.com/kubernetes-sigs/promo-tools/issues/682 -->
<!-- Search query: https://github.com/kubernetes-sigs/promo-tools/issues?q=is%3Aissue+is%3Aclosed+Release+k-sigs%2Fpromo-tools -->
Previous promo-tools release cut issue: <!-- insert previous release cut issue number -->

- [ ] Release (signed tag and GitHub release, see RELEASE.md):  <!-- Example: https://github.com/kubernetes-sigs/promo-tools/releases/tag/vX.Y.Z -->
- [ ] Staging image vX.Y.Z-0 (post-kpromo-push-image run of the tag, see RELEASE.md):  <!-- Example: https://prow.k8s.io/?job=post-kpromo-push-image -->
- [ ] Image promotion:  <!-- Example: https://github.com/kubernetes/k8s.io/pull/4446 -->
- [ ] Rollout:  <!-- Example: https://github.com/kubernetes/test-infra/pull/27963 -->

cc @kubernetes-sigs/release-engineering 
