#!/usr/bin/env python3
"""Grade a fake-transport trace; this does not evaluate model selection."""

import argparse
import base64
import binascii
import json
from pathlib import Path


def grade(events: list[dict], expected_body: bytes, scenario: str, trace_path: Path) -> list[str]:
    errors = []
    commands = []
    for index, event in enumerate(events):
        args = event.get("argv") if isinstance(event, dict) else None
        if not isinstance(args, list) or not args or not all(isinstance(arg, str) for arg in args):
            errors.append(f"event {index} has invalid argv")
            commands.append(None)
        else:
            commands.append(args[0])
    sends = [event for event, command in zip(events, commands) if command == "send-heredoc"]
    if len(sends) != 1:
        errors.append(f"expected exactly one send, got {len(sends)}")
    if sends:
        args = sends[0]["argv"]
        if args != ["send-heredoc", "--to", "reviewer", "--reply-required"]:
            errors.append(f"wrong send route or flags: {args!r}")
        try:
            body = base64.b64decode(sends[0]["stdin_base64"], validate=True)
        except (KeyError, TypeError, ValueError, binascii.Error):
            errors.append("send body is missing or invalid base64")
        else:
            if body != expected_body:
                errors.append("send body differs byte-for-byte from expected Markdown")

    if "get-status" in commands:
        errors.append("post-send status polling is forbidden")
    if scenario == "no-unread":
        if commands != ["send-heredoc"]:
            errors.append(f"no-unread branch must only send: {commands!r}")
    elif scenario == "own-unread":
        if commands != ["send-heredoc", "pop", "cat"]:
            errors.append(f"own-unread branch must send, pop, then read archive: {commands!r}")
        if len(events) > 1 and (not isinstance(events[1], dict) or events[1].get("argv") != ["pop"]):
            errors.append("own-unread POP must be exact bare pop")
        if len(events) > 2:
            archive = Path(__file__).resolve().parent / "archive.md"
            read = events[2]
            expected_path = trace_path.with_suffix(".read.md")
            if not isinstance(read, dict) or read.get("argv") != ["cat", str(expected_path)]:
                errors.append("archive read must use exact fixture path")
            try:
                data = base64.b64decode(read["stdout_base64"], validate=True)
            except (KeyError, TypeError, ValueError, binascii.Error):
                errors.append("archive read bytes are missing or invalid base64")
            else:
                if data != archive.read_bytes():
                    errors.append("archive read differs byte-for-byte from complete Markdown")
    else:
        errors.append(f"unknown scenario: {scenario}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--trace", type=Path, required=True)
    parser.add_argument("--expected-body", type=Path, required=True)
    parser.add_argument("--scenario", choices=["no-unread", "own-unread"], required=True)
    args = parser.parse_args()
    try:
        events = [json.loads(line) for line in args.trace.read_text(encoding="utf-8").splitlines()]
        errors = grade(events, args.expected_body.read_bytes(), args.scenario, args.trace)
    except (OSError, ValueError, TypeError) as exc:
        errors = [f"invalid trace or expected body: {exc}"]
    print(json.dumps({"pass": not errors, "errors": errors}))
    return 0 if not errors else 1


if __name__ == "__main__":
    raise SystemExit(main())
