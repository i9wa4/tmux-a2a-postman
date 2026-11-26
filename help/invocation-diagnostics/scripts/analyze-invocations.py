#!/usr/bin/env python3
"""Read explicitly selected diagnostic logs without modifying state."""

import argparse
from collections import Counter
import json
import math
from pathlib import Path
import statistics


def read_invocation(path):
    result = {"path": str(path), "incomplete": True, "identity": {}, "events": [], "reason": "invalid"}
    if path.is_symlink() or path.parent.is_symlink() or not path.is_file():
        return result
    with path.open("rb") as stream:
        raw = stream.read(128 * 1024 + 1)
    if len(raw) > 128 * 1024:
        result["reason"] = "oversized"
        return result
    lines = raw.splitlines(keepends=True)
    identifier = None
    for seq, line in enumerate(lines):
        if not line.endswith(b"\n"):
            result["reason"] = "truncated_tail"
            break
        try:
            event = json.loads(line)
            if not isinstance(event, dict) or event.get("schema_version") != 1 or event.get("seq") != seq:
                break
            identifier = identifier or event.get("invocation_id")
            if not isinstance(identifier, str) or event.get("invocation_id") != identifier:
                break
            result["events"].append(event)
        except (ValueError, UnicodeError):
            result["reason"] = "malformed_line"
            break
    events = result["events"]
    if events and events[0].get("event") == "start":
        result["identity"] = events[0].get("identity", {})
        if not isinstance(result["identity"], dict):
            result["identity"] = {}
            return result
        result["invocation_id"] = identifier
        terminal = events[-1]
        if (len(events) == len(lines) and terminal.get("event") == "end"
                and not any(e.get("event") == "end" for e in events[:-1])
                and isinstance(terminal.get("metrics", {}), dict)
                and isinstance(terminal.get("facts", {}), dict)
                and isinstance(terminal.get("outcome", "unknown"), str)):
            duration = terminal.get("duration_ms")
            if type(duration) in (int, float) and math.isfinite(duration) and duration >= 0:
                result.update(incomplete=False, reason="", duration_ms=duration, outcome=terminal.get("outcome", "unknown"),
                              facts=terminal.get("facts", {}), metrics=terminal.get("metrics", {}))
    return result


def summarize(rows):
    complete = [r for r in rows if not r["incomplete"]]
    durations = sorted(r["duration_ms"] for r in complete)
    outcomes = Counter(r.get("outcome", "incomplete") for r in rows)
    return {"samples": len(rows), "complete": len(complete), "incomplete": len(rows) - len(complete),
            "outcomes": dict(outcomes), "median_ms": statistics.median(durations) if durations else None,
            "p95_ms": durations[max(0, math.ceil(len(durations) * .95) - 1)] if durations else None,
            "outliers": [{"path": r["path"], "invocation_id": r.get("invocation_id"), "duration_ms": r["duration_ms"],
                          "outcome": r["outcome"], "facts": r.get("facts"), "metrics": r.get("metrics")}
                         for r in sorted(complete, key=lambda r: r["duration_ms"], reverse=True)[:5]],
            "incomplete_files": [{"path": r["path"], "reason": r["reason"], "last_phase": next(
                (e.get("phase") for e in reversed(r["events"]) if e.get("event") == "phase_start"), None)}
                for r in rows if r["incomplete"]]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("logs", nargs="+", type=Path, help="explicit authorized own logs directory or file")
    for name in ["date", "node", "command", "build", "config"]:
        parser.add_argument("--" + name)
    parser.add_argument("--min-summary-files", type=int)
    args = parser.parse_args()
    paths = set()
    for root in args.logs:
        if root.is_file():
            paths.add(root)
        elif root.is_dir() and not root.is_symlink():
            paths.update(root.glob("*/inv-*.jsonl"))
    selected, excluded, unreadable = [], Counter(), []
    seen_files = set()
    for path in sorted(paths):
        if args.date and path.parent.name != args.date:
            excluded["date"] += 1
            continue
        try:
            # Explicit roots may name the same file through a workspace alias.
            # Keep final-file/day symlink rejection in read_invocation intact.
            if not path.is_symlink() and not path.parent.is_symlink():
                info = path.stat()
                identity = (info.st_dev, info.st_ino)
                if identity in seen_files:
                    continue
                seen_files.add(identity)
            row = read_invocation(path)
        except OSError:
            unreadable.append(str(path))
            continue
        identity = row["identity"]
        filters = {"node": args.node, "command": args.command, "build_commit": args.build, "config_fingerprint": args.config}
        if any(value is not None and identity.get(key) != value for key, value in filters.items()):
            excluded["identity_mismatch_or_unknown"] += 1
            continue
        if args.min_summary_files is not None:
            count = row.get("metrics", {}).get("summary_files_scanned", {}).get("count")
            if count is None:
                excluded["unknown_workload"] += 1
                continue
            if count < args.min_summary_files:
                excluded["workload"] += 1
                continue
        selected.append(row)
    print(json.dumps({**summarize(selected), "excluded": dict(excluded), "unreadable": unreadable}, indent=2))


if __name__ == "__main__":
    main()
