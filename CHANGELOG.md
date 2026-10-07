# Changelog

## 1.0.1

- Added tested provider alert rules matching the previous five-scrape, 60% error
  rate and p50 >3s thresholds, plus exporter/MTR alerts and Alertmanager guidance.
- Added promtool firing/recovery tests for 12 alerting scenarios.
- Refreshed runtime bundle; exporter images remain the tested 1.0.0 images.

## 1.0.0

- Configurable HTTP providers and exact healthy status codes in strict YAML.
- Docker images for HTTP/mTLS and optional MTR, listening on port 9106.
- Non-root execution, runtime-only configuration and certificate mounts.
- Fresh scrape-time probes with bounded concurrency and graceful shutdown.
- Complete-body latency, timeout/error categories, enforced IP family and explicit redirect policy.
- MTR JSON parsing with destination verification, correct average and base units.
- Build information, health/readiness endpoints, English/Persian guides and migration notes.
- Local-fixture tests for HTTP, IPv4/IPv6, TLS/mTLS, metrics and container behavior.
