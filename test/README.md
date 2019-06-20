# E2E Tests

This directory has logic that allows the creation of deterministic Docker images
using Bazel for e2e testing.

The entrypoint is the e2e.go binary which reads test data from test/tests.yaml.
To run it, invoke it from the project root with:

```
bazel run //test:e2e -- -tests=$PWD/test/tests.yaml -repo-root $PWD -key-file <GCP-account-secret-key.json>
```

The e2e.go binary's `-tests` flag takes a YAML that has test cases. Each test case has 2 main parts:

1. an embedded promoter manifest
1. a before/after snapshot of GCRs to compare actual promoter runs with expected results

In other words, for each test, e2e.go invokes the `cip` binary to perform a
promotion run. After the promotion finishes, it checks expected GCR snapshots
against the actual repositories (as defined in the embedded promoter manifest)
to make sure that they do indeed match the expected snapshots as defined in the
test case.
