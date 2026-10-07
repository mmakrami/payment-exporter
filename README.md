# Payment Exporter

A configurable Prometheus exporter for HTTP availability checks. Each provider
has an explicit list of healthy HTTP status codes, a timeout, and optional TLS,
mTLS and MTR settings. The default listen port is **9106**.

[راهنمای فارسی](README.fa.md)

## Quick start from GitHub

```bash
git clone https://github.com/mmakrami/payment-exporter.git
cd payment-exporter
cp config.example.yml config.yml
# Edit config.yml: set your URLs and their verified healthy status codes.
docker compose build
docker compose run --rm payment-exporter --config.check
docker compose up -d
curl http://localhost:9106/-/healthy
```

Docker builds the exporter; a local Go installation is not required. For MTR,
use `docker compose -f compose.mtr.yaml` for these three Compose commands and
enable MTR only for the desired providers. This repository does not install
Prometheus or Grafana.

For ready-made Linux amd64 images, download the **quickstart archive** from
[GitHub Releases](https://github.com/mmakrami/payment-exporter/releases). It
contains only the runtime files, both images, Prometheus examples and the
Grafana dashboard. Extract it, follow the included `README.fa.md` or `README.md`,
and run `docker load` before `docker compose up -d`.

## Quick start with the delivered image

```bash
docker load -i payment-exporter-1.0.0-images.tar.gz
cp config.example.yml config.yml
# Edit config.yml: set your URLs and their verified healthy status codes.

docker run --rm \
  -v "$PWD/config.yml:/etc/payment-exporter/config.yml:ro" \
  paystar/payment-exporter:1.0.0 --config.check \
  --config.file=/etc/payment-exporter/config.yml

docker run -d --name payment-exporter --restart unless-stopped \
  --read-only --cap-drop=ALL --security-opt=no-new-privileges:true \
  --pids-limit=128 --memory=256m --cpus=1 \
  -p 9106:9106 \
  -v "$PWD/config.yml:/etc/payment-exporter/config.yml:ro" \
  paystar/payment-exporter:1.0.0

curl http://localhost:9106/-/healthy
curl http://localhost:9106/metrics
```

Alternatively, `docker compose up -d` uses the supplied `compose.yaml`. To build
from source, use `docker compose up -d --build` or `make docker`.

The image tags are local release names; loading the archive does not require a
registry account. To distribute through your registry, retag and push the tested
image to your own namespace. The included archive is for **Linux amd64**.

## Configuration

```yaml
global:
  timeout: 10s
  max_concurrency: 16
  max_response_body_bytes: 1048576

providers:
  - name: TokenAPI
    url: https://payments.example.com/token
    method: POST
    headers:
      Content-Type: application/json
    body: '{}'
    expected_status_codes: [401]
    timeout: 8s
    ip_protocol: ip4
    follow_redirects: false
```

`expected_status_codes: [401]` means **only 401** is healthy. A response of 200,
403, 404, 429 or 500 is unhealthy for that provider. The default, when the field
is omitted, is `[200]`. Use an explicit list for several acceptable codes, for
example `[200, 204]`. An empty list is rejected. Check response codes against the
actual endpoint contract; they are not inferred from the provider name.

| Setting | Default | Meaning |
|---|---|---|
| `global.timeout` | `10s` | Default total HTTP deadline, including DNS, connection, TLS, redirects and body reading |
| `global.max_concurrency` | `16` | Maximum provider checks in parallel; extra providers wait for a slot |
| `global.max_response_body_bytes` | `1048576` | Maximum decoded response body size; exceeding it fails the HTTP probe |
| `providers[].name` | required | Unique, stable metric label, at most 128 bytes |
| `providers[].url` | required | Absolute HTTP or HTTPS URL; no embedded user credentials |
| `method` | `GET` | HTTP method |
| `headers` | empty | Request headers; a `Host` header sets the virtual host |
| `headers_from_env` | empty | Map of header name to environment variable name |
| `body` | empty | Literal request body; supports YAML block strings for XML/JSON |
| `timeout` | global timeout | Per-provider total HTTP deadline, at most `2m` |
| `expected_status_codes` | `[200]` | Exact HTTP status allow-list |
| `ip_protocol` | `ip4` | `ip4` forces IPv4; `ip6` forces IPv6; `auto` permits both |
| `follow_redirects` | `false` | Follow at most 10 same-origin redirects; cross-origin redirects are blocked |
| `tls.insecure_skip_verify` | `false` | Explicitly disable server certificate verification |
| `tls.ca_file` | empty | Add a custom CA bundle to the system trust store |
| `tls.cert_file` / `tls.key_file` | empty | Client certificate and private key for mTLS; both are required together |
| `tls.server_name` | empty | Override certificate verification name and TLS SNI |
| `mtr.enabled` | `false` | Enable optional destination network diagnostics |
| `mtr.protocol` | `icmp` | `icmp` or `tcp`; TCP uses the port in the URL or 80/443 |
| `mtr.cycles` | `3` | MTR report cycles, from 1 to 20 |
| `mtr.timeout` | `15s` | Total MTR deadline, including destination DNS resolution; at most `1m` |

Unknown YAML fields, duplicate names/keys/status codes, missing environment
variables and unreadable TLS files are rejected before the server starts.
Changes to config, credentials or certificates require a container restart.
Relative certificate paths are resolved against the config file's directory.
TLS requires version 1.2 or newer. HTTP probes are direct: proxy environment
variables such as `HTTP_PROXY` and `HTTPS_PROXY` are intentionally ignored.

### Credentials and mTLS

Keep credentials outside the source tree and image. For a secret header:

```yaml
headers_from_env:
  Authorization: PROVIDER_AUTHORIZATION
```

Pass the **complete header value**, e.g. `Bearer ...`, in that environment
variable (`docker run --env-file .env ...`, or add `env_file: .env` to Compose).
The `.env`, live `config.yml` and certificate files are excluded from Git and
from the Docker build context. Logs and metric labels do not include URLs,
request bodies or header values.

For a combined client certificate/private key PEM:

```yaml
tls:
  cert_file: /run/secrets/provider.pem
  key_file: /run/secrets/provider.pem
  insecure_skip_verify: false
```

Mount it with `-v "$PWD/secrets/provider.pem:/run/secrets/provider.pem:ro"`, or add
the same volume to Compose. The container runs as **65532:65532**. Grant this
UID/group read access to the mounted file and access to its containing directory;
keep the private key protected (for example group ownership 65532 with mode 0640).
Never copy a private key into the image. A custom CA uses the same mount pattern.

### Optional MTR image

Use `paystar/payment-exporter:1.0.0-mtr` only when a provider enables MTR:

```bash
docker run -d --name payment-exporter --restart unless-stopped \
  --read-only --cap-drop=ALL --cap-add=NET_RAW \
  --pids-limit=128 --memory=256m --cpus=1 \
  -p 9106:9106 \
  -v "$PWD/config.yml:/etc/payment-exporter/config.yml:ro" \
  paystar/payment-exporter:1.0.0-mtr
```

Or use the standalone `docker compose -f compose.mtr.yaml up -d` configuration.
To build that variant: `make docker-mtr`.

The MTR image also runs as UID 65532. Its packet helper has only the `NET_RAW`
file capability. The container bounding set must include `NET_RAW`, and
`no-new-privileges` must be **off** for the helper to acquire this capability.
Neither privileged mode nor `NET_ADMIN` is needed. The standard HTTP image drops
all capabilities and enables `no-new-privileges`.

MTR reads structured JSON and uses the destination's `Avg`, not its `Last`.
It resolves a destination IP and verifies that IP appears in the report. Failure
or an unreachable destination produces `mtr_success=0`; loss and latency samples
are omitted rather than repeating old values. MTR checks are independent of HTTP
health and run in parallel with HTTP for each provider. MTR and HTTP can resolve
a load-balanced hostname to different IPs; MTR is a route diagnostic, not a trace
of the exact HTTP connection. ICMP filtering can prevent a valid ICMP report.

## Scraping and operational behavior

Every `GET /metrics` performs a **fresh probe of each configured provider**.
There is no background polling. Control cadence using Prometheus's
`scrape_interval` (30s is a useful starting value). A response body must be read
successfully, within the deadline and size limit, before a probe is healthy.
Connections can be reused between scrapes, so latency describes actual probe
duration rather than always a new TCP/TLS handshake.

Choose a `scrape_timeout` longer than the complete batch of probes. For `N`
providers and concurrency `C`, a conservative bound is:

```text
ceil(N / C) × max(all HTTP timeouts, all enabled MTR timeouts) + headroom
```

The included Prometheus example uses 30s interval and 25s timeout. For a larger
batch or longer configured timeouts, increase both accordingly. HTTP health and
readiness endpoints do not contact providers. Config validation checks local
files and MTR installation; it does not send requests or prove remote reachability.

One metrics request is accepted at a time. A concurrent scrape returns **503**,
preventing duplicate payment requests and an unbounded request queue. Use one
scraping Prometheus instance per exporter instance, or run a separate exporter
for each HA Prometheus replica. Additional manual `/metrics` visits also probe
the configured endpoints. An abandoned scrape may finish its bounded probes;
SIGTERM cancels in-flight probes and shuts down the server gracefully.

| Endpoint | Purpose |
|---|---|
| `/` | Exporter landing page |
| `/metrics` | Prometheus/OpenMetrics metrics and fresh probes |
| `/-/healthy` | Exporter process is serving HTTP; used by Docker healthcheck |
| `/-/ready` | Exporter has started with validated configuration |

These HTTP endpoints do not implement authentication. Publish the port on the
monitoring network; use a firewall or authenticated reverse proxy for other
access. Bind to localhost instead with `-p 127.0.0.1:9106:9106` if appropriate.

## Metrics

All provider metrics carry only the stable `provider` label, with `code` or
`reason` where listed. URLs and secrets are never metric labels.

| Metric | Type / unit | Meaning |
|---|---|---|
| `payment_provider_up` | gauge / 0 or 1 | Current HTTP probe succeeded and its status was allowed |
| `payment_provider_http_status_code` | gauge | Current response code; 0 when no usable response was received |
| `payment_provider_probe_duration_seconds` | gauge / seconds | Current HTTP probe duration including response body |
| `payment_provider_last_probe_timestamp_seconds` | gauge / Unix seconds | Completion time of the current HTTP probe |
| `payment_provider_probe_timeout_seconds` | gauge / seconds | Configured HTTP timeout |
| `payment_provider_latency_seconds` | histogram / seconds | Cumulative HTTP latency for successes and failures |
| `payment_provider_request_total{code}` | counter | Completed HTTP probes per response code |
| `payment_provider_probe_errors_total{reason}` | counter | Failures: request, timeout, canceled, dns, tls, transport, body, body_too_large or status |
| `payment_provider_mtr_success` | gauge / 0 or 1 | Current MTR produced valid destination statistics |
| `payment_provider_mtr_packet_loss_ratio` | gauge / 0–1 | Destination loss, only when MTR succeeds |
| `payment_provider_mtr_avg_latency_seconds` | gauge / seconds | Destination average RTT, only when MTR succeeds |
| `payment_exporter_build_info` | gauge / 1 | Exporter version, revision and Go version |

Go runtime and process metrics are also included. Prometheus's own `up` measures
the scrape of the **exporter**; `payment_provider_up` measures each **provider**.
Counters/histograms reset when the exporter restarts. The timestamp is a gauge
value, not a custom Prometheus sample timestamp.

Example queries:

```promql
# Currently unhealthy HTTP endpoints
payment_provider_up == 0

# Mean probe duration over 5 minutes
rate(payment_provider_latency_seconds_sum[5m])
  / rate(payment_provider_latency_seconds_count[5m])

# Fraction of failed probes, independent of whether the response is 2xx or 4xx
sum by (job, instance, provider) (rate(payment_provider_probe_errors_total[5m]))
  / sum by (job, instance, provider) (rate(payment_provider_request_total[5m]))
```

See `examples/prometheus.yml`, `examples/alerts.yml` and `MIGRATION.md`.

## Grafana dashboard

Import [`grafana/payment-exporter.dashboard.json`](grafana/payment-exporter.dashboard.json)
using **Dashboards → New → Import**. Choose the Prometheus datasource scraping
your exporter, then choose its job. The initial job selection is `payment-exporter`.
The exporter remains on port **9106**; Grafana queries Prometheus, not this port.

The dashboard provides a failure-first status table, current health and freshness,
HTTP health history, p95 latency, observed success ratios, failure categories and
response codes. Optional MTR and exporter resource diagnostics start collapsed.
It uses only built-in panels, with no extra plugins or recording rules.
Unknown data is never interpreted as healthy, and failed MTR measurements do
not appear as zero packet loss. HTTP 401/404 are interpreted through the
configured probe health rather than colored as failures automatically.

The Classic JSON schema and panel settings were checked against Grafana 10.4.0's
official schemas. All dashboard PromQL queries and synthetic outage/freshness
scenarios were checked with `promtool`. Actual Grafana import and rendering have
not been tested. See [`grafana/README.md`](grafana/README.md) for details.

## Build and verification

Requires Go 1.25+ (the pinned build toolchain is 1.27.1), Docker, and optionally
Docker Compose v2. The Docker build uses a static Go binary and Alpine with CA
certificates; config and credentials are mounted at runtime.

```bash
make check        # meaningful HTTP/TLS/mTLS/MTR/metrics tests with race detection + vet
make build        # bin/payment-exporter
make docker       # standard image
make docker-mtr   # optional MTR image
make save         # compressed archive containing both image tags

# Container integration suite; uses local fixtures, never payment providers.
go build -o bin/http-fixture ./testdata/fixture
python3 scripts/integration.py
```

CLI flags: `--config.file`, `--web.listen-address`, `--config.check`,
`--log.level`, `--log.format`, `--version`, `--healthcheck`. Configuration errors
exit with status 1. Use `--log.level=debug` to see bounded probe failure
categories without request secrets.

The design follows Prometheus guidance on [YAML configuration and fresh
scrapes](https://prometheus.io/docs/instrumenting/writing_exporters/) and
[metric naming and base units](https://prometheus.io/docs/practices/naming/).
This exporter originated at Paystar. It is an independent project and is not an
official Prometheus or Grafana product.
