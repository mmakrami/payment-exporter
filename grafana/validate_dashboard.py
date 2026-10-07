#!/usr/bin/env python3
"""Check dashboard structure and its actual PromQL with synthetic Prometheus data.

Uses the existing official Prometheus image; does not install or run Grafana.
"""
import json
from pathlib import Path
import re
import subprocess
import tempfile

from build_dashboard import (
    dashboard, STATE, HTTP, WINDOW_SUCCESS, BUDGET, mtr_success, grouped, metric,
)

ROOT = Path(__file__).resolve().parents[1]
IMAGE = "prom/prometheus:v3.15.0"


def walk(panels):
    for panel in panels:
        yield panel
        yield from walk(panel.get("panels", []))


def substitute(expr):
    for old, new in {
        "${job:regex}": "payment-exporter", "${instance:regex}": ".*",
        "${provider:regex}": ".*", "$freshness": "180",
        "$__rate_interval": "2m", "$__range": "5m",
    }.items():
        expr = expr.replace(old, new)
    assert "$" not in expr, expr
    return expr


def sample(instance, value, provider="Shared", **labels):
    labels = dict(job="payment-exporter", instance=instance, provider=provider, **labels)
    text = "{" + ",".join(f'{k}="{v}"' for k, v in sorted(labels.items())) + "}"
    return {"labels": text, "value": value}


def input_series(name, instance, values, provider="Shared", **labels):
    labels = dict(job="payment-exporter", instance=instance, **labels)
    if provider is not None:
        labels["provider"] = provider
    suffix = "{" + ",".join(f'{k}="{v}"' for k, v in sorted(labels.items())) + "}"
    return {"series": name + suffix, "values": values}


def expr_test(expr, expected):
    return {"expr": substitute(expr), "eval_time": "5m", "exp_samples": expected}


def structural_validation():
    saved = json.loads((ROOT / "dist/payment-exporter-grafana-dashboard.json").read_text())
    assert saved == dashboard, "Regenerate the dashboard before validation"
    all_panels = list(walk(saved["panels"]))
    ids = [p["id"] for p in all_panels]
    assert len(ids) == len(set(ids))
    kinds = {"stat", "table", "timeseries", "state-timeline", "bargauge", "barchart", "text", "row"}
    for p in all_panels:
        assert p["type"] in kinds
        g = p["gridPos"]
        assert 0 <= g["x"] < 24 and g["w"] > 0 and g["x"] + g["w"] <= 24
        assert g["h"] > 0 and g["y"] >= 0
        targets = p.get("targets", [])
        assert len({t["refId"] for t in targets}) == len(targets)
        for t in targets:
            assert t["datasource"] == {"type": "prometheus", "uid": "${datasource}"}
            assert '${job:regex}' in t["expr"] and '${instance:regex}' in t["expr"]
            if "payment_provider_" in t["expr"]:
                assert '${provider:regex}' in t["expr"]
            assert not re.search(r'\bor\s+(?:vector\(0\)|0)\b', t["expr"])
    # Root panels are visible; nested panels appear only when a row is expanded.
    def overlap(a, b):
        return a["x"] < b["x"] + b["w"] and b["x"] < a["x"] + a["w"] and a["y"] < b["y"] + b["h"] and b["y"] < a["y"] + a["h"]
    for group in [saved["panels"]] + [p["panels"] for p in all_panels if p["type"] == "row" and p.get("panels")]:
        for i, p in enumerate(group):
            for q in group[i + 1:]:
                assert not overlap(p["gridPos"], q["gridPos"]), (p["title"], q["title"])
    assert {v["name"] for v in saved["templating"]["list"]} == {"datasource", "job", "instance", "provider", "freshness"}
    expressions = {substitute(t["expr"]) for p in all_panels for t in p.get("targets", [])}
    return len(all_panels), expressions


def behavioral_tests():
    series = []
    # Identical provider names on different instances must remain independent.
    for instance, health, scrape, stamp in [
        ("vm-a:9106", "1 1 1 1 1 1", "1 1 1 1 1 1", "0 60 120 180 240 300"),
        ("vm-b:9106", "0 0 0 0 0 0", "1 1 1 1 1 1", "0 60 120 180 240 300"),
        ("vm-c:9106", "1 1 1 1 1 1", "1 1 1 1 0 0", "0 60 120 180 180 180"),
        ("vm-stale:9106", "1 1 1 1 1 1", "1 1 1 1 1 1", "0 0 0 0 0 0"),
        ("vm-clock:9106", "1 1 1 1 1 1", "1 1 1 1 1 1", "600 660 720 780 840 900"),
        ("vm-removed:9106", "1 1 stale _ _ _", "1 1 1 1 1 1", "0 60 stale _ _ _"),
    ]:
        series.extend([
            input_series("payment_provider_up", instance, health),
            input_series("up", instance, scrape, provider=None),
            input_series("payment_provider_last_probe_timestamp_seconds", instance, stamp),
        ])
    state_expected = [sample(i, v) for i, v in [
        ("vm-a:9106", 0), ("vm-b:9106", 3), ("vm-c:9106", 2),
        ("vm-stale:9106", 1), ("vm-clock:9106", 1), ("vm-removed:9106", 1),
    ]]
    # 401 is healthy by configuration; 200 with a body failure is unhealthy.
    series.extend([
        input_series("payment_provider_http_status_code", "vm-a:9106", "401 401 401 401 401 401"),
        input_series("payment_provider_http_status_code", "vm-b:9106", "200 200 200 200 200 200"),
        input_series("payment_provider_probe_duration_seconds", "vm-a:9106", "2 2 2 2 2 2"),
        input_series("payment_provider_probe_timeout_seconds", "vm-a:9106", "10 10 10 10 10 10"),
        input_series("payment_provider_mtr_success", "vm-a:9106", "0 0 0 0 0 0"),
        # A retained old loss metric must be hidden after measurement failure.
        input_series("payment_provider_mtr_packet_loss_ratio", "vm-a:9106", "0 0 0 0 0 0"),
        input_series("payment_provider_mtr_success", "vm-b:9106", "1 1 1 1 1 1"),
        input_series("payment_provider_mtr_packet_loss_ratio", "vm-b:9106", "0.02 0.02 0.02 0.02 0.02 0.02"),
    ])
    loss = f"{grouped(metric('payment_provider_mtr_packet_loss_ratio'))} and on (job, instance, provider) ({mtr_success} == 1)"
    health_tests = {"name": "state, freshness, identity, HTTP codes and MTR validity", "interval": "1m", "input_series": series,
        "promql_expr_test": [
            expr_test(STATE, state_expected),
            expr_test(HTTP, [sample("vm-a:9106", 1), sample("vm-b:9106", 0)]),
            expr_test(f"sum({STATE} == bool 0)", [{"labels": "{}", "value": 1}]),
            expr_test(f"sum({STATE} == bool 3)", [{"labels": "{}", "value": 1}]),
            expr_test(f"sum(({STATE} > bool 0) * ({STATE} < bool 3))", [{"labels": "{}", "value": 4}]),
            expr_test(BUDGET, [sample("vm-a:9106", 0.2)]),
            expr_test(loss, [sample("vm-b:9106", 0.02)]),
        ]}
    ratios = []
    for instance, count, errors in [
        ("healthy:9106", "0 2 4 6 8 10", "0 0 0 0 0 0"),
        ("failing:9106", "0 2 4 6 8 10", "0 2 4 6 8 10"),
        ("reset:9106", "0 2 4 0 2 4", "0 1 2 0 1 2"),
        ("no-probes:9106", "0 0 0 0 0 0", "0 0 0 0 0 0"),
    ]:
        ratios.extend([
            input_series("payment_provider_latency_seconds_count", instance, count),
            input_series("payment_provider_probe_errors_total", instance, errors, reason="status"),
        ])
    ratio_tests = {"name": "observed success handles resets and excludes zero attempts", "interval": "1m", "input_series": ratios,
        "promql_expr_test": [expr_test(WINDOW_SUCCESS, [sample("healthy:9106", 1), sample("failing:9106", 0), sample("reset:9106", 0.5)])]}
    return [health_tests, ratio_tests]


def main():
    count, expressions = structural_validation()
    tests = {"rule_files": [], "evaluation_interval": "1m", "tests": [
        {"name": "parse every actual dashboard query", "interval": "1m", "input_series": [],
         "promql_expr_test": [{"expr": e, "eval_time": "5m", "exp_samples": []} for e in sorted(expressions)]},
        *behavioral_tests(),
    ]}
    with tempfile.TemporaryDirectory(prefix="payment-dashboard-tests-") as tmp:
        path = Path(tmp) / "tests.yml"
        path.write_text(json.dumps(tests, indent=2))  # JSON is also valid YAML.
        path.chmod(0o644)
        Path(tmp).chmod(0o755)
        subprocess.run(["docker", "run", "--rm", "--network=none", "--entrypoint=/bin/promtool",
                        "-v", f"{tmp}:/tests:ro", IMAGE, "test", "rules", "/tests/tests.yml"], check=True)
    print(f"PASS: {count} built-in panels, {len(expressions)} distinct PromQL queries, layout and behavioral checks")


if __name__ == "__main__":
    main()
