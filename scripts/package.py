#!/usr/bin/env python3
"""Package only allow-listed source and verified release artifacts."""

import argparse
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]
SOURCE_ENTRIES = [
    "cmd", "internal", "examples", "scripts", "testdata", ".github", "grafana",
    "go.mod", "go.sum", "Dockerfile", "compose.yaml", "compose.mtr.yaml",
    "Makefile", ".dockerignore", ".gitignore", "config.example.yml",
    "README.md", "README.fa.md", "MIGRATION.md", "CHANGELOG.md",
    "TEST-REPORT.md",
]


def source_files():
    for entry in SOURCE_ENTRIES:
        path = ROOT / entry
        if not path.exists():
            raise SystemExit("Missing release source: " + entry)
        for candidate in sorted(path.rglob("*") if path.is_dir() else [path]):
            if candidate.is_file() and "__pycache__" not in candidate.parts:
                if candidate.suffix in {".pem", ".key", ".crt", ".pyc"}:
                    raise SystemExit("Unexpected secret or generated file in release source")
                yield candidate


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", default="1.0.0")
    parser.add_argument("--release-version", help="Bundle version; defaults to the unchanged exporter image version")
    args = parser.parse_args()
    destination = ROOT / "dist"
    destination.mkdir(exist_ok=True)
    release_version = args.release_version or args.version
    prefix = "payment-exporter-" + release_version
    images = destination / ("payment-exporter-" + args.version + "-images.tar.gz")
    required = [images, destination / "integration-report.json", destination / "unit-tests.txt", ROOT / "bin/payment-exporter"]
    for path in required:
        if not path.exists():
            raise SystemExit("Missing verified artifact: " + str(path))
    report = json.loads((destination / "integration-report.json").read_text())
    if not report["results"] or not all(test["passed"] for test in report["results"]):
        raise SystemExit("Integration suite did not pass")
    tags = ["paystar/payment-exporter:" + args.version, "paystar/payment-exporter:" + args.version + "-mtr"]
    image_info = json.loads(subprocess.check_output(["docker", "image", "inspect", *tags], text=True))
    tested_ids = report.get("image_ids", {})
    current_ids = {tag: image["Id"] for image in image_info for tag in image["RepoTags"] if tag in tags}
    if tested_ids != current_ids:
        raise SystemExit("Current image IDs differ from the images verified by integration tests")
    with tarfile.open(images, "r:gz") as archive:
        manifest = json.load(archive.extractfile("manifest.json"))
        saved_ids = {}
        for entry in manifest:
            config_hash = hashlib.sha256(archive.extractfile(entry["Config"]).read()).hexdigest()
            for tag in entry.get("RepoTags", []):
                if tag in tags:
                    saved_ids[tag] = "sha256:" + config_hash
        if saved_ids != tested_ids:
            raise SystemExit("Saved image archive does not match the verified image IDs")
    metadata = {
        "version": release_version,
        "exporter_version": args.version,
        "platform": "linux/amd64",
        "port": 9106,
        "images": [{"tags": image["RepoTags"], "id": image["Id"], "size_bytes": image["Size"], "user": image["Config"]["User"]} for image in image_info],
        "source_sha256": {str(path.relative_to(ROOT)): sha256(path) for path in source_files()},
    }
    (destination / "release-manifest.json").write_text(json.dumps(metadata, indent=2) + "\n")
    sources = destination / (prefix + "-source.tar.gz")
    with tarfile.open(sources, "w:gz") as archive:
        for path in source_files():
            archive.add(path, arcname=prefix + "/" + str(path.relative_to(ROOT)), recursive=False)
    binary = destination / (prefix + "-linux-amd64.tar.gz")
    with tarfile.open(binary, "w:gz") as archive:
        archive.add(ROOT / "bin/payment-exporter", arcname="payment-exporter")
        archive.add(ROOT / "config.example.yml", arcname="config.example.yml")
        archive.add(ROOT / "README.md", arcname="README.md")
        archive.add(ROOT / "README.fa.md", arcname="README.fa.md")

    # One group-ready bundle: source, images, examples and verification evidence.
    bundle = destination / (prefix + "-bundle.tar.gz")
    with tarfile.open(bundle, "w:gz") as archive:
        for path in source_files():
            archive.add(path, arcname=prefix + "/" + str(path.relative_to(ROOT)), recursive=False)
        for path in [images, destination / "integration-report.json", destination / "unit-tests.txt", destination / "release-manifest.json"]:
            archive.add(path, arcname=prefix + "/" + path.name, recursive=False)
        # A fresh non-secret configuration, never the operator's live config.yml.
        archive.add(ROOT / "config.example.yml", arcname=prefix + "/config.yml", recursive=False)
        payload = {str(path.relative_to(ROOT)): sha256(path) for path in source_files()}
        for path in [images, destination / "integration-report.json", destination / "unit-tests.txt", destination / "release-manifest.json"]:
            payload[path.name] = sha256(path)
        payload["config.yml"] = sha256(ROOT / "config.example.yml")
        checksum_data = "".join(digest + "  " + name + "\n" for name, digest in sorted(payload.items())).encode()
        info = tarfile.TarInfo(prefix + "/PAYLOAD-SHA256SUMS")
        info.size = len(checksum_data)
        info.mode = 0o644
        archive.addfile(info, io.BytesIO(checksum_data))
    # Small operator bundle: no source, build tools, test fixtures or local logs.
    runtime_entries = [
        "compose.yaml", "compose.mtr.yaml", "config.example.yml",
        "README.md", "README.fa.md", "examples/prometheus.yml", "examples/alerts.yml", "examples/ALERTING.md",
        "grafana/payment-exporter.dashboard.json", "grafana/README.md",
    ]
    quickstart = destination / (prefix + "-quickstart.tar.gz")
    with tarfile.open(quickstart, "w:gz") as archive:
        payload = {}
        for name in runtime_entries:
            path = ROOT / name
            if not path.is_file():
                raise SystemExit("Missing runtime file: " + name)
            archive.add(path, arcname=prefix + "/" + name, recursive=False)
            payload[name] = sha256(path)
        archive.add(images, arcname=prefix + "/" + images.name, recursive=False)
        payload[images.name] = sha256(images)
        data = "".join(digest + "  " + name + "\n" for name, digest in sorted(payload.items())).encode()
        info = tarfile.TarInfo(prefix + "/PAYLOAD-SHA256SUMS")
        info.size = len(data)
        info.mode = 0o644
        archive.addfile(info, io.BytesIO(data))
    dashboard = destination / "payment-exporter-grafana-dashboard.json"
    dashboard.write_bytes((ROOT / "grafana/payment-exporter.dashboard.json").read_bytes())
    artifacts = [images, sources, binary, bundle, quickstart, dashboard,
                 destination / "release-manifest.json", destination / "integration-report.json"]
    checksums = destination / "SHA256SUMS"
    checksums.write_text("".join(sha256(path) + "  " + path.name + "\n" for path in artifacts))
    for path in artifacts + [checksums]:
        print(path.name, path.stat().st_size, "bytes")


if __name__ == "__main__":
    main()
