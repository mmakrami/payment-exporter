#!/usr/bin/env python3
"""Validate alert syntax and firing behavior using official promtool."""
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
IMAGE = "prom/prometheus:v3.15.0"


def main():
    for args in [("check", "rules", "examples/alerts.yml"),
                 ("test", "rules", "testdata/alerts.test.yml")]:
        subprocess.run(["docker", "run", "--rm", "--network=none",
                        "-v", f"{ROOT}:/src:ro", "-w", "/src",
                        "--entrypoint=/bin/promtool", IMAGE, *args], check=True)
    print("PASS: alert rules and firing/recovery scenarios")


if __name__ == "__main__":
    main()
