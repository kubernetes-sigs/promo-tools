# E2E Testing Overview

This directory has logic that allows the creation of deterministic Docker images
using Bazel for e2e testing.

There are 2 flavors of e2e tests, each in its own subfolder:

1. cip: tests promotion logic (`e2e.go`)
2. cip-auditor: tests the auditing mechanism logic (`cip-auditor-e2e.go`)

For both flavors, the testing binary uses test data in a `tests.yaml` to run the necessary tests.

## Running tests

Here's a way to invoke the tests from your local checkout, against your own GCP test repository:

```console
export CIP_E2E_KEY_FILE=path/to/secret/creds.json

# For "cip" e2e tests.
make test-e2e-cip

# For "cip-auditor" e2e tests.
make test-e2e-cip-auditor
```

### cip (e2e.go)

The test cases are defined in `./cip/tests.yaml`. Each test case has 2 parts:

1. an embedded promoter manifest
2. a before/after snapshot of GCRs to compare actual promoter runs with expected
   results

In other words, for each test, `e2e.go` invokes the `cip` binary to perform a
promotion run. After the promotion finishes, it checks expected GCR snapshots
against the actual repositories (as defined in the embedded promoter manifest)
to make sure that they do indeed match the expected snapshots as defined in the
test case.

### cip-auditor (cip-auditor-e2e.go)

The test cases are defined in `./cip-auditor/tests.yaml`. Each test case has 5
parts

1. Clear GCP (test project) logs.
2. Set up preliminary GCR state.
3. Spin up the auditor on Cloud Run.
4. Modify GCR state (which should trigger the auditor to audit this change).
5. Check GCP (test project) logs on Stackdriver to check how the auditor
   behaved.
