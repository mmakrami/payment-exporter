#!/usr/bin/env python3
"""Exercise the actual release images against local fixtures, not payment APIs."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def command(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT).strip()


def wait_health(base):
    for _ in range(80):
        try:
            with urllib.request.urlopen(base + "/-/healthy", timeout=2) as response:
                if response.status == 200:
                    return
        except (OSError, urllib.error.URLError, TimeoutError):
            time.sleep(0.15)
    raise AssertionError("exporter never became healthy")


def scrape(base):
    with urllib.request.urlopen(base + "/metrics", timeout=25) as response:
        assert response.status == 200
        return response.read().decode()


def metric(text, name, **labels):
    for line in text.splitlines():
        match = re.fullmatch(re.escape(name) + r"(?:\{(.*?)\})? ([^ ]+)", line)
        if not match:
            continue
        actual = dict(re.findall(r'(\w+)="([^"]*)"', match[1] or ""))
        if actual == {key: str(value) for key, value in labels.items()}:
            return float(match[2])
    raise AssertionError(f"missing metric {name} {labels}")


def certificates(directory):
    # All keys are short-lived test material and are deleted after the suite.
    command("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-keyout", str(directory / "ca.key"), "-out", str(directory / "ca.pem"), "-days", "1", "-subj", "/CN=integration CA")
    for name, extension in [("server", "subjectAltName=DNS:fixture\nextendedKeyUsage=serverAuth\n"), ("client", "extendedKeyUsage=clientAuth\n")]:
        command("openssl", "req", "-new", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-keyout", str(directory / (name + ".key")), "-out", str(directory / (name + ".csr")), "-subj", "/CN=" + name)
        extensions = directory / (name + ".ext")
        extensions.write_text(extension)
        command("openssl", "x509", "-req", "-in", str(directory / (name + ".csr")), "-CA", str(directory / "ca.pem"), "-CAkey", str(directory / "ca.key"), "-CAcreateserial", "-out", str(directory / (name + ".pem")), "-days", "1", "-extfile", str(extensions))
    (directory / "combined.pem").write_text((directory / "client.pem").read_text() + (directory / "client.key").read_text())
    os.chmod(directory, 0o755)
    for path in directory.iterdir():
        os.chmod(path, 0o644)  # Test-only keys readable by non-root fixture/exporter.


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--image", default="paystar/payment-exporter:1.0.0")
    parser.add_argument("--mtr-image", default="paystar/payment-exporter:1.0.0-mtr")
    parser.add_argument("--report", default=str(ROOT / "dist/integration-report.json"))
    args = parser.parse_args()
    fixture_binary = ROOT / "bin/http-fixture"
    assert fixture_binary.is_file(), "first build bin/http-fixture (see README)"
    name = "payment-exporter-test-" + uuid.uuid4().hex[:8]
    containers = []
    results = []
    base = "http://127.0.0.1:9106"
    # Use a normal bridge, matching the delivered Compose files. Some Docker
    # firewall backends intentionally block host-published ports on --internal
    # networks. All configured destinations here are still local fixture aliases.
    command("docker", "network", "create", name)
    try:
        work_directory = ROOT / "dist"
        work_directory.mkdir(exist_ok=True)
        # Keep bind mounts under the project: Docker Desktop and isolated host
        # sessions may have different /tmp directories from the Docker daemon.
        with tempfile.TemporaryDirectory(prefix="integration-", dir=work_directory) as temporary:
            directory = Path(temporary)
            os.chmod(directory, 0o755)
            secrets = directory / "secrets"
            secrets.mkdir()
            certificates(secrets)
            fixture = name + "-fixture"
            command("docker", "run", "-d", "--name", fixture, "--network", name, "--network-alias", "fixture", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "-v", str(fixture_binary) + ":/fixture:ro", "-v", str(secrets) + ":/secrets:ro", "--entrypoint", "/fixture", args.image, "--cert=/secrets/server.pem", "--key=/secrets/server.key", "--ca=/secrets/ca.pem")
            containers.append(fixture)
            config = directory / "config.yml"
            config.write_text("""global:
  timeout: 2s
  max_concurrency: 4
  max_response_body_bytes: 1024
providers:
  - {name: Healthy, url: 'http://fixture:8080/healthy', expected_status_codes: [200]}
  - name: Auth
    url: http://fixture:8080/auth
    method: POST
    headers: {Content-Type: application/json}
    headers_from_env: {Authorization: TEST_PROVIDER_AUTH}
    body: '{}'
    expected_status_codes: [401]
  - {name: NotFound, url: 'http://fixture:8080/notfound', expected_status_codes: [404]}
  - {name: WrongStatus, url: 'http://fixture:8080/rate-limit', expected_status_codes: [200]}
  - {name: SlowBody, url: 'http://fixture:8080/slow-body', timeout: 100ms}
  - {name: LargeBody, url: 'http://fixture:8080/oversized'}
  - {name: Redirect, url: 'http://fixture:8080/redirect', expected_status_codes: [302]}
  - name: MutualTLS
    url: https://fixture:8443/mtls
    expected_status_codes: [401]
    tls:
      ca_file: /run/secrets/ca.pem
      cert_file: /run/secrets/combined.pem
      key_file: /run/secrets/combined.pem
""")
            os.chmod(config, 0o644)
            exporter = name + "-http"
            run_args = ["docker", "run", "-d", "--name", exporter, "--network", name, "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--pids-limit=128", "--memory=256m", "-p", "127.0.0.1:9106:9106", "-v", str(config) + ":/etc/payment-exporter/config.yml:ro", "-v", str(secrets) + ":/run/secrets:ro", "-e", "TEST_PROVIDER_AUTH=Bearer integration-only", args.image]
            command(*run_args)
            containers.append(exporter)
            wait_health(base)
            # Give the fixture's DNS alias and TLS listener time to start without
            # causing probes from health checks themselves.
            fixture_ready = command("docker", "exec", exporter, "/bin/payment-exporter", "--healthcheck")
            first = scrape(base)
            expected = {"Healthy": (1, 200), "Auth": (1, 401), "NotFound": (1, 404), "WrongStatus": (0, 429), "SlowBody": (0, 200), "LargeBody": (0, 200), "Redirect": (1, 302), "MutualTLS": (1, 401)}
            for provider, (healthy, status) in expected.items():
                assert metric(first, "payment_provider_up", provider=provider) == healthy, provider
                assert metric(first, "payment_provider_http_status_code", provider=provider) == status, provider
                results.append({"test": provider, "up": healthy, "status": status, "passed": True})
            assert metric(first, "payment_provider_probe_errors_total", provider="SlowBody", reason="timeout") == 1
            assert metric(first, "payment_provider_probe_errors_total", provider="LargeBody", reason="body_too_large") == 1
            assert metric(first, "payment_provider_probe_duration_seconds", provider="SlowBody") >= 0.08
            second = scrape(base)
            assert metric(second, "payment_provider_request_total", provider="Auth", code="401") == 2
            (ROOT / "dist/http-metrics.prom").write_text(second)
            inspect = json.loads(command("docker", "inspect", exporter))[0]
            assert inspect["Config"]["User"] == "65532:65532"
            assert inspect["HostConfig"]["ReadonlyRootfs"]
            assert inspect["HostConfig"]["CapDrop"] == ["ALL"]
            assert "no-new-privileges:true" in inspect["HostConfig"]["SecurityOpt"]
            command("docker", "exec", exporter, "/bin/payment-exporter", "--config.file=/etc/payment-exporter/config.yml", "--config.check")
            logs = command("docker", "logs", exporter)
            assert "Bearer integration-only" not in logs
            assert "PRIVATE KEY" not in logs
            started = time.monotonic()
            command("docker", "stop", "--time=10", exporter)
            assert time.monotonic() - started < 5
            assert json.loads(command("docker", "inspect", exporter))[0]["State"]["ExitCode"] == 0
            command("docker", "rm", exporter)
            containers.remove(exporter)
            results.append({"test": "container_security_counters_config_shutdown", "passed": True})

            # Actual MTR packet helper, as non-root, with NET_RAW only.
            config.write_text("""providers:
  - name: NetworkFixture
    url: http://fixture:8080/healthy
    expected_status_codes: [200]
    mtr: {enabled: true, protocol: tcp, cycles: 3, timeout: 15s}
""")
            mtr_exporter = name + "-mtr"
            command("docker", "run", "-d", "--name", mtr_exporter, "--network", name, "--read-only", "--cap-drop=ALL", "--cap-add=NET_RAW", "--pids-limit=128", "--memory=256m", "-p", "127.0.0.1:9106:9106", "-v", str(config) + ":/etc/payment-exporter/config.yml:ro", args.mtr_image)
            containers.append(mtr_exporter)
            wait_health(base)
            text = scrape(base)
            assert metric(text, "payment_provider_up", provider="NetworkFixture") == 1
            assert metric(text, "payment_provider_mtr_success", provider="NetworkFixture") == 1
            assert metric(text, "payment_provider_mtr_packet_loss_ratio", provider="NetworkFixture") == 0
            assert metric(text, "payment_provider_mtr_avg_latency_seconds", provider="NetworkFixture") >= 0
            (ROOT / "dist/mtr-metrics.prom").write_text(text)
            inspect = json.loads(command("docker", "inspect", mtr_exporter))[0]
            assert inspect["Config"]["User"] == "65532:65532"
            caps = {cap.removeprefix("CAP_") for cap in inspect["HostConfig"]["CapAdd"]}
            assert caps == {"NET_RAW"}
            assert not inspect["HostConfig"]["Privileged"]
            results.append({"test": "real_mtr_tcp_nonroot_net_raw_only", "passed": True})

            command("docker", "stop", "--time=10", mtr_exporter)
            command("docker", "rm", mtr_exporter)
            containers.remove(mtr_exporter)
            invalid = directory / "invalid.yml"
            invalid.write_text("providers:\n  - {name: test, url: 'http://fixture:8080', expected_status_codes: [999]}\n")
            invalid_result = subprocess.run(["docker", "run", "--rm", "-v", str(invalid) + ":/etc/payment-exporter/config.yml:ro", args.image, "--config.check", "--config.file=/etc/payment-exporter/config.yml"], capture_output=True, text=True)
            assert invalid_result.returncode == 1
            results.append({"test": "invalid_config_refuses_start", "passed": True})

            image_ids = {tag: command("docker", "image", "inspect", tag, "--format", "{{.Id}}") for tag in [args.image, args.mtr_image]}
            report = {"port": 9106, "http_image": args.image, "mtr_image": args.mtr_image, "image_ids": image_ids, "architecture": "linux/amd64", "uses_payment_providers": False, "results": results}
            destination = Path(args.report)
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_text(json.dumps(report, indent=2) + "\n")
            print(json.dumps(report, indent=2))
    except Exception:
        for container in containers:
            print("Container logs:", container)
            subprocess.run(["docker", "logs", container], check=False)
        raise
    finally:
        for container in reversed(containers):
            subprocess.run(["docker", "rm", "-f", container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(["docker", "network", "rm", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
