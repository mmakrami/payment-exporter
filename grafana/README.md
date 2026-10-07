# Payment Exporter — Grafana dashboard

Import [`payment-exporter.dashboard.json`](payment-exporter.dashboard.json) using
**Dashboards → New → Import**, then select the Prometheus datasource that scrapes
Payment Exporter 1.0.0 on port **9106**. Select the correct exporter job after
import; `payment-exporter` is the initial selection. No Grafana installation,
plugins, recording rules, or changes to the exporter are needed.

The portable delivery file is `dist/payment-exporter-grafana-dashboard.json`.
The dashboard uses the Classic JSON format and built-in Grafana panels. Its
schema and panel settings have been checked against Grafana 10.4.0's official
schemas. Actual import and rendering in a Grafana server have not been tested.

## Operational layout

- Current scrape health, healthy/unhealthy/unknown counts, observed probe success,
  and recent p95 latency.
- Provider table with failures first, explicit state, HTTP status, latency,
  deadline utilization, observed success, and completion age. Click a provider
  name to filter that provider and instance.
- Health timeline, latency and success trends, current latency and timeout budget.
- Failure categories, HTTP response distribution, failure and probe rates.
- Optional MTR history, destination loss and RTT, initially collapsed.
- Exporter scrape history and runtime resources, initially collapsed.
- Embedded explanations and operational guide, initially collapsed.

The datasource, job, instance, provider and freshness limit are selectable.
The default time range is six hours with a 30-second dashboard refresh. HTTP
401/404 may be healthy if configured; the dashboard uses `payment_provider_up`
for health rather than inferring health from response codes. Scrape failure,
stale data, missing probes, and failed MTR measurements never imply health or
zero packet loss. Probe success refers to observed monitoring attempts, not
payment transactions or an SLA. MTR diagnostics are independent of HTTP health.

Identities use job + instance + provider. The freshness limit defaults to
180 seconds; choose a larger limit for deliberately slower scrape intervals.
A probe timestamp more than five seconds in the future is considered unknown.
Historical ratios exclude missing scrapes; window counts are extrapolated
estimates. Histograms estimate p95 and include failed probes.

## Rebuild and verification

```sh
python3 grafana/build_dashboard.py
python3 grafana/validate_dashboard.py
```

The validation script uses the official `prom/prometheus:v3.15.0` image to run
`promtool test rules` with network access disabled. It checks all dashboard
queries and synthetic scenarios covering identical names on different VMs,
stale/missing data, scrape loss, future timestamps, configured HTTP health,
counter resets, zero attempts, timeout budgets and invalid MTR results.
It does not start Grafana or probe production payment endpoints.

Schema references:

- [Classic dashboard schema](https://github.com/grafana/grafana/blob/v10.4.0/kinds/dashboard/dashboard_kind.cue)
- [Built-in panel schemas](https://github.com/grafana/grafana/tree/v10.4.0/public/app/plugins/panel)
- [Shared panel field and option schemas](https://github.com/grafana/grafana/tree/v10.4.0/packages/grafana-schema/src/common)
