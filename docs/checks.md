# Vulnerability Scanning

The promoter can scan staging images for vulnerabilities before promotion. This
ensures that production images meet a minimum security standard.

## Scanner Interface

Vulnerability scanning is abstracted behind the `vuln.Scanner` interface
(`promoter/image/vuln/scanner.go`):

```go
type Scanner interface {
	Scan(ctx context.Context, ref string) (*ScanResult, error)
}
```

The `ScanResult` contains a list of `Vulnerability` entries, each with a
`Severity` level and metadata. The `ExceedsSeverity()` helper checks whether
any vulnerability meets or exceeds a given threshold.

### Severity Levels

| Value | Level |
|-------|-------|
| 0 | UNSPECIFIED |
| 1 | MINIMAL |
| 2 | LOW |
| 3 | MEDIUM |
| 4 | HIGH |
| 5 | CRITICAL |

### Available Implementations

- **`GrafeasScanner`** (`vuln/grafeas.go`) — Wraps the GCP Container Analysis
  (Grafeas) API. Queries vulnerability occurrences for a given image reference
  and maps Grafeas severity levels to the portable `Severity` type. Supports
  a `FixableOnly` mode that only reports vulnerabilities with known fixes.

- **`NoopScanner`** (`vuln/noop.go`) — Returns empty results. Used for testing
  and non-GCP environments.

## Usage

The `--vuln-severity-threshold` flag controls the severity threshold. When set,
the promoter runs a vulnerability scan and rejects images with vulnerabilities
at or above the threshold:

```console
kpromo cip --thin-manifest-dir=<dir> --vuln-severity-threshold=4 --confirm
```

This would reject any image with HIGH (4) or CRITICAL (5) vulnerabilities.

Setting the threshold to `0` (default) disables the severity gate.

## Integration With Prow

The [*pull-k8sio-cip-vuln*][k8sio-presubmits] Prow job runs vulnerability
checks on pull requests that modify promoter manifests. The job uses
`--vuln-severity-threshold` to enforce the desired policy.

[k8sio-presubmits]: https://git.k8s.io/test-infra/config/jobs/kubernetes/sig-k8s-infra/releng/artifact-promotion-presubmits.yaml
