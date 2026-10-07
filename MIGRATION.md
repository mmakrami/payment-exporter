# Migration from payment_exporter_v3

The new release preserves the `provider` label and these metric names:

- `payment_provider_up`
- `payment_provider_request_total{provider,code}`
- `payment_provider_latency_seconds_bucket`, `_sum`, `_count`

Semantics are intentionally more precise:

| v3 behavior | New behavior |
|---|---|
| Port 9105 | Port 9106 |
| Providers hardcoded into Go | Providers loaded from YAML |
| 30s background probes | Fresh probes on each Prometheus scrape; set `scrape_interval: 30s` |
| Generic `status < 500` | Exact per-provider allow-list, default `[200]` |
| Endpoint-specific status rules only in comments | Explicit healthy status codes in config |
| Nominal IPv4 preference | Enforced `tcp4`/`tcp6`, or `auto` |
| Latency ends at response headers | Latency includes complete body reading |
| Body read errors ignored | Read errors, body timeout and oversize response fail health |
| Failed client certificate load only logged | Exporter refuses to start |
| Text MTR parser uses first RTT column | Structured JSON uses destination `Avg` |
| Last MTR row assumed to be destination | Destination IP must appear in report |
| Failed MTR retains old values | `mtr_success=0`; loss/latency omitted |
| Default redirects followed | Redirects disabled by default; optional same-origin redirects |

MTR metric names and units change to Prometheus base units:

| Old | New | Conversion |
|---|---|---|
| `payment_provider_mtr_packet_loss_percent` | `payment_provider_mtr_packet_loss_ratio` | Multiply new value by 100 for a percentage display |
| `payment_provider_mtr_avg_latency_ms` | `payment_provider_mtr_avg_latency_seconds` | Multiply new value by 1000 for a milliseconds display |

Update Grafana panels and alerts that reference the old MTR metrics. Historical
HTTP latency values before the migration describe a different measurement
boundary. Counter resets at deployment are expected and handled by `rate()`.

## Existing endpoints

Set each endpoint's healthy status codes from its verified response contract.
A previous generic `<500` rule cannot establish which exact responses prove
availability. For example, `[401]` or `[404]` can be used when the owning team
has confirmed that response for the configured probe request. A legacy 2xx rule
must be converted to an explicit list if all those codes are actually accepted.
No production endpoint URLs, credentials or provider-specific contracts are
included. No live payment-provider requests were needed for this migration or
the included fixture tests.

Old source cookies and private keys are not embedded in this release. Use
`headers_from_env` and read-only mounted certificate files when needed.

## Rollout and rollback

Run the new exporter on 9106 alongside the existing service on 9105. Add a new
Prometheus job, compare verified response rules and latency behavior, then update
dashboards/alerts. The old service can remain available during the transition.
Stopping the new container and restoring the old scrape target provides rollback.
