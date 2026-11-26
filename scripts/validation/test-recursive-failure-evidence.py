#!/usr/bin/env python3
"""A partial actor trace must not hide the recursive fixture's first failure."""

import json
import os
import pathlib
import subprocess
import sys
import tempfile


with tempfile.TemporaryDirectory(prefix="pm-recursive-evidence-", dir="/tmp") as owned:
    root = pathlib.Path(owned) / "fixture"
    output = pathlib.Path(owned) / "results.json"
    try:
        run = subprocess.run(
            [sys.argv[1], "--case", "chain", "--timeout", "0", "--output", str(output)],
            env={**os.environ, "POSTMAN_SMOKE_TEST_TRUNCATED_ACTION": str(root)},
            capture_output=True,
            text=True,
            timeout=60,
        )
        logged = [
            item["failure_report"]
            for line in run.stdout.splitlines()
            if "failure_report" in (item := json.loads(line))
        ]
        assert run.returncode == 1, (run.returncode, run.stdout, run.stderr)
        assert len(logged) == 1, run.stdout
        report = json.loads(output.read_text())["cases"][0]
        assert pathlib.Path(report["root"]) == root.resolve()
        assert report["error"].startswith("timed out:"), report["error"]
        assert report["error"] == logged[0]["error"]
        assert (root / "report.json").is_file()
        assert report["hop_events"][0]["message_id"].endswith("-control")
        assert report["hop_trace_errors"][0]["line"] == 2
        assert subprocess.run(
            [sys.argv[2], "-S", str(root / "s"), "has-session"],
            capture_output=True,
            check=False,
        ).returncode != 0, "private tmux server was not cleaned up"
    finally:
        # Root identity is known even when launch, parsing or assertions fail.
        try:
            subprocess.run(
                [sys.argv[2], "-S", str(root / "s"), "kill-server"],
                capture_output=True,
                check=False,
                timeout=5,
            )
        except (OSError, subprocess.TimeoutExpired):
            pass

print("truncated action trace preserves failure report and private cleanup")
