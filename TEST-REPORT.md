# Release verification — 1.0.0

Verified locally on **2026-10-07**, on Linux amd64, using Go 1.27.1 and Docker
Engine 29.1.3. Default exporter port: **9106**.

## Automated results

| Check | Result |
|---|---|
| `go test -race -count=1 -v ./...` | PASS: 18 top-level tests; 45 passing entries including subtests |
| `go vet ./...` | PASS |
| `go mod verify` | PASS: all module checksums verified |
| Standard and MTR image builds | PASS |
| `scripts/integration.py` against actual release images | PASS: 11 result groups |
| Both Compose configurations | PASS: `docker compose config -q` |
| Prometheus config / 4 alert rules | PASS: official `promtool` from Prometheus v3.15.0 |
| HTTP and MTR metric exposition lint | PASS: `promtool check metrics` |
| Live Compose service on 9106 | PASS: Docker healthy; health endpoint returns 200 |
| Live non-payment example.com probe | PASS: HTTP 200; provider up=1 |
| Grafana dashboard structure and grid layout | PASS: 33 built-in panels and sections |
| Dashboard PromQL and synthetic scenarios | PASS: 30 distinct queries; scrape loss, stale/missing data, clock skew, counter resets and MTR validity |
| Grafana Classic JSON and built-in panel settings | PASS: official Grafana 10.4.0 schemas; 52 panel/field configurations |

The container suite uses a local deterministic HTTP/mTLS fixture. It verifies:

- Exact allowed 200, 401 and 404 responses; 429 is unhealthy when only 200 is allowed.
- POST method, JSON body and a header supplied through an environment variable.
- Body-read timeout produces HTTP status 200 but provider up=0 and a timeout error.
- An oversized response body fails the probe.
- Redirect handling with redirects disabled.
- Actual verified mTLS with a combined certificate/key PEM and a custom CA.
- Counters increment once per scrape; config checking sends no remote probes.
- Read-only filesystem, UID/GID 65532, dropped capabilities and no-new-privileges for HTTP.
- Clean SIGTERM shutdown with exit status 0.
- Real MTR TCP destination measurement as non-root, using only NET_RAW and no privileged mode.
- Invalid configuration refuses startup with exit status 1.

Additional unit tests verify same-origin/cross-origin redirects, body transfer
latency, truncated bodies, TLS verification, missing certificates, enforced
IPv4/IPv6, bounded parallelism, overlapping scrapes, cancellation and MTR JSON
destination/average parsing. IPv6 loopback tests passed on this laptop.

## Release images

| Image | Image ID | Uncompressed size |
|---|---|---:|
| `paystar/payment-exporter:1.0.0` | `sha256:0ba6a4c9ec1303f9eda5c76f90bca696d47a678159b7c58dc292217d59d67bfe` | 21,153,644 bytes |
| `paystar/payment-exporter:1.0.0-mtr` | `sha256:ba1550f96f3a1459aa352af123443b8f2bfd2016ca55cb4ce200874494ffc793` | 21,901,249 bytes |

Both images run as **65532:65532** and share the same static exporter binary.
The standard image requires no capabilities; MTR adds only NET_RAW. The image
archive contains both tags. Packaging compares its image configuration hashes
against the exact IDs recorded by the integration suite.

See `integration-report.json`, `unit-tests.txt`, `release-manifest.json` and
`PAYLOAD-SHA256SUMS` inside the complete bundle. Outer artifacts are covered by
the separate `SHA256SUMS` file. SHA-256 checksums provide integrity checks; the
artifacts are local release files and have not been pushed to a registry.

## Scope and reproducibility

No live payment-provider endpoints were used. The original payment contracts,
production certificate and original deployed server were not available for live
verification. Configure their confirmed healthy status codes before deployment.
Only the delivered Linux amd64 platform was built and tested. GitHub Actions
repeats the exporter tests, image builds, container integration suite and
dashboard validation on repository pushes and pull requests. The table above
records local verification; current hosted results are available in
[GitHub Actions](https://github.com/mmakrami/payment-exporter/actions/workflows/ci.yml).
Grafana was not installed. Actual dashboard import and rendering in a running
Grafana instance have not been verified.

On this laptop, direct toolchain downloads and some module downloads encountered
network restrictions. Go was obtained from the pinned official Go Docker image.
The image build used host networking and a module proxy fallback, with Go's
checksum verification enabled:

```bash
make docker docker-mtr DOCKER_BUILD_FLAGS=--network=host \
  GOPROXY='https://proxy.golang.org|https://goproxy.cn'
```

Docker Compose and Buildx plugins were obtained from the official Docker CLI
image. Application probes still use their documented direct connections and do
not depend on the module-download proxy.
