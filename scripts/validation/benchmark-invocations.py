#!/usr/bin/env python3
"""Opt-in isolated real-CLI baseline/candidate benchmark; no agent/model API."""

import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import shutil
import statistics
import subprocess
import tempfile
import time


def distribution(values):
    values = sorted(values)
    return {"samples": len(values), "median_ms": statistics.median(values),
            "p95_ms": values[max(0, (len(values) * 95 + 99) // 100 - 1)]}


def bench(binary, samples, history, concurrency):
    with tempfile.TemporaryDirectory(prefix="pm964-bench-", dir="/tmp") as temporary:
        root = Path(temporary).resolve()
        home = root / "home"
        work = home / "work"
        work.mkdir(parents=True, mode=0o700)
        socket = str(root / "s")
        config = root / "postman.md"
        config.write_text("## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n")
        def tmux(*args):
            return subprocess.check_output(["tmux", "-S", socket, *args], text=True, timeout=5).strip()
        try:
            worker = tmux("-f", "/dev/null", "new-session", "-d", "-s", "work", "-c", str(work), "-P", "-F", "#{pane_id}", "exec sleep 180")
            peer = tmux("split-window", "-d", "-t", worker, "-P", "-F", "#{pane_id}", "exec sleep 180")
            tmux("select-pane", "-t", worker, "-T", "worker")
            tmux("select-pane", "-t", peer, "-T", "peer")
            env = {**os.environ, "HOME": str(home), "ZDOTDIR": str(home), "XDG_STATE_HOME": str(root / "state"),
                   "XDG_CONFIG_HOME": str(home / ".config"), "TMUX": socket + ",0,0"}
            def invoke(command, pane=worker, body="", extra=()):
                started = time.perf_counter_ns()
                result = subprocess.run([str(binary), command, "--config", str(config), *extra], input=body,
                                        text=True, capture_output=True, env={**env, "TMUX_PANE": pane}, cwd=work, timeout=15)
                elapsed = (time.perf_counter_ns() - started) / 1e6
                output = json.loads(result.stdout)
                if result.returncode != 0:
                    raise RuntimeError(f"benchmark operation failed: {command}: {output.get('reason')}")
                return elapsed
            invoke("pop")
            groups = {}
            for retained in [0, history]:
                if retained:
                    for number in range(history):
                        invoke("send-heredoc", body=f"deterministic benchmark item {number}\n", extra=("--to", "peer"))
                        invoke("pop", pane=peer)
                groups[f"pop_history_{retained}"] = distribution([invoke("pop") for _ in range(samples)])
                groups[f"status_history_{retained}"] = distribution([invoke("get-status") for _ in range(samples)])
                batches = []
                for _ in range(samples):
                    started = time.perf_counter_ns()
                    with ThreadPoolExecutor(max_workers=concurrency) as pool:
                        list(pool.map(lambda _: invoke("pop"), range(concurrency)))
                    batches.append((time.perf_counter_ns() - started) / 1e6)
                groups[f"parallel_{concurrency}_history_{retained}"] = distribution(batches)
            files = sorted((root / "state").glob("tmux-a2a-postman/*/agents/*/logs/*/inv-*.jsonl"))
            incomplete, outcomes, captures = 0, {}, {}
            for path in files:
                events = [json.loads(line) for line in path.read_text().splitlines()]
                end = events[-1]
                if end.get("event") != "end":
                    incomplete += 1
                outcomes[end.get("outcome", "incomplete")] = outcomes.get(end.get("outcome", "incomplete"), 0) + 1
                capture = end.get("facts", {}).get("capture_result", "not_attempted")
                captures[capture] = captures.get(capture, 0) + 1
            return {"binary": str(binary), "samples_per_group": samples, "history_messages": history,
                    "concurrency": concurrency, "groups": groups, "log_files": len(files),
                    "log_bytes": sum(p.stat().st_size for p in files), "incomplete_logs": incomplete,
                    "log_outcomes": outcomes, "capture_outcomes": captures}
        finally:
            subprocess.run(["tmux", "-S", socket, "kill-server"], capture_output=True, timeout=5, check=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", required=True, type=Path)
    parser.add_argument("--candidate", required=True, type=Path)
    parser.add_argument("--samples", type=int, default=12)
    parser.add_argument("--history", type=int, default=100)
    parser.add_argument("--concurrency", type=int, default=4)
    args = parser.parse_args()
    if not shutil.which("tmux") or not 2 <= args.samples <= 100 or not 1 <= args.history <= 500 or not 1 <= args.concurrency <= 16:
        parser.error("require tmux, samples 2..100, history 1..500, concurrency 1..16")
    result = {name: bench(binary.resolve(), args.samples, args.history, args.concurrency)
              for name, binary in [("baseline", args.baseline), ("candidate", args.candidate)]}
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
