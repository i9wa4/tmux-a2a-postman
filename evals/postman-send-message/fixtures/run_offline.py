#!/usr/bin/env python3
"""Scripted, no-model dispatcher probe for the offline Postman fixture.

This is a dispatcher test, not an OS sandbox. The scripted caller runs in this
process; it is never given a generic subprocess, file, network, or shell tool.
"""

import base64
import hashlib
import json
import stat
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parent
BODY = (ROOT / "body.md").read_bytes()
ARCHIVE = (ROOT / "archive.md").read_bytes()
SEND = ["send-heredoc", "--to", "reviewer", "--reply-required"]


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class DispatchDenied(Exception):
    pass


class OfflineDispatcher:
    """Exact-argument transport adapter with runner-owned in-memory receipts."""

    def __init__(self, directory: Path, scenario: str):
        if scenario not in {"no-unread", "own-unread"}:
            raise ValueError("invalid scenario")
        self.directory = directory.resolve(strict=True)
        self.scenario = scenario
        self.fake_trace = self.directory / "fake.jsonl"
        self.archive_path = self.fake_trace.with_suffix(".read.md")
        self.phase = "new"
        self.denied = False
        self.receipts = []
        self.invocations = 0

    def _deny(self, executable, argv, stdin, reason):
        self.denied = True
        self.phase = "denied"
        self.receipts.append({
            "executable": executable, "argv": argv, "stdin_sha256": digest(stdin),
            "decision": "denied", "reason": reason,
            "side_effects": self.invocations,
            "prior_invocations": self.invocations,
        })
        raise DispatchDenied(reason)

    def _invoke(self, executable, argv, stdin):
        script = ROOT / ("tmux-a2a-postman" if executable == "tmux-a2a-postman" else "cat")
        env = {
            "FAKE_POSTMAN_TRACE": str(self.fake_trace),
            "FAKE_POSTMAN_SCENARIO": self.scenario,
            "PATH": "/usr/bin:/bin",
        }
        result = subprocess.run(
            [sys.executable, str(script), *argv], input=stdin, env=env,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
        )
        self.invocations += 1
        receipt = {
            "executable": executable, "script": str(script.resolve(strict=True)),
            "script_sha256": digest(script.read_bytes()), "argv": argv,
            "stdin_base64": base64.b64encode(stdin).decode("ascii"),
            "exit_code": result.returncode,
            "stdout_base64": base64.b64encode(result.stdout).decode("ascii"),
            "stdout_len": len(result.stdout), "stdout_sha256": digest(result.stdout),
            "stderr_base64": base64.b64encode(result.stderr).decode("ascii"),
            "decision": "executed",
        }
        self.receipts.append(receipt)
        if result.returncode != 0:
            self.denied = True
            self.phase = "denied"
            raise DispatchDenied("fixture tool exited nonzero")
        return result.stdout

    def _decode_response(self, executable, argv, stdin, output):
        try:
            reply = json.loads(output)
        except (UnicodeDecodeError, json.JSONDecodeError):
            self._deny(executable, argv, stdin, "malformed fixture JSON response")
        if not isinstance(reply, dict):
            self._deny(executable, argv, stdin, "fixture response is not an object")
        return reply

    def dispatch(self, executable: str, argv: list[str], stdin: bytes = b"") -> bytes:
        """Return complete tool stdout; this is the caller-delivered byte result."""
        if not isinstance(executable, str) or not isinstance(argv, list) or not all(
            isinstance(arg, str) for arg in argv
        ) or not isinstance(stdin, bytes):
            self._deny(executable, argv, b"", "invalid structured request")
        if self.denied:
            self._deny(executable, argv, stdin, "terminal denial; no fallback")
        if self.phase == "new" and executable == "tmux-a2a-postman" and argv == SEND and stdin == BODY:
            output = self._invoke(executable, argv, stdin)
            reply = self._decode_response(executable, argv, stdin, output)
            if (not isinstance(reply.get("sent"), str) or not reply["sent"]
                    or reply.get("status") != "queued"):
                self._deny(executable, argv, stdin, "send response is not queued mail")
            expected = 1 if self.scenario == "own-unread" else 0
            self_state = reply.get("self_state")
            if not isinstance(self_state, dict):
                self._deny(executable, argv, stdin, "send self-state is not an object")
            if self_state.get("known") is not True:
                self._deny(executable, argv, stdin, "send self-state is not known")
            count = self_state.get("own_unread_count")
            if type(count) is not int or count != expected:
                self._deny(executable, argv, stdin, "send self-state mismatch")
            self.phase = "sent"
            return output
        if (self.phase == "sent" and self.scenario == "own-unread"
                and executable == "tmux-a2a-postman" and argv == ["pop"] and stdin == b""):
            output = self._invoke(executable, argv, stdin)
            reply = self._decode_response(executable, argv, stdin, output)
            message_id = reply.get("message_id")
            if (reply.get("status") != "message" or not isinstance(message_id, str)
                    or not message_id):
                self._deny(executable, argv, stdin, "pop response is not a claimed message")
            if reply.get("markdown_absolute_path") != str(self.archive_path):
                self._deny(executable, argv, stdin, "pop archive identity mismatch")
            self._check_archive()
            self.phase = "popped"
            return output
        if (self.phase == "popped" and executable == "cat"
                and argv == [str(self.archive_path)] and stdin == b""):
            self._check_archive()
            output = self._invoke(executable, argv, stdin)
            if output != ARCHIVE:
                self._deny(executable, argv, stdin, "cat delivered altered archive bytes")
            self.receipts[-1]["delivered_after_eof"] = True
            self.receipts[-1]["archive_path"] = str(self.archive_path)
            self.receipts[-1]["archive_sha256"] = digest(ARCHIVE)
            self.phase = "read"
            return output
        self._deny(executable, argv, stdin, "request outside exact phase/argv policy")

    def _check_archive(self):
        if self.archive_path.parent != self.directory:
            self._deny("cat", [str(self.archive_path)], b"", "archive escaped fixture root")
        try:
            metadata = self.archive_path.lstat()
            if not stat.S_ISREG(metadata.st_mode) or self.archive_path.is_symlink():
                self._deny("cat", [str(self.archive_path)], b"", "archive is not a regular file")
            data = self.archive_path.read_bytes()
        except OSError:
            self._deny("cat", [str(self.archive_path)], b"", "archive missing or unreadable")
        if data != ARCHIVE:
            self._deny("cat", [str(self.archive_path)], b"", "archive bytes differ from fixture")

    def complete(self) -> bool:
        expected = "read" if self.scenario == "own-unread" else "sent"
        return not self.denied and self.phase == expected


def scripted_probe(directory: Path, scenario: str) -> dict:
    """Exercise both branches without importing or contacting any model SDK."""
    runner = OfflineDispatcher(directory, scenario)
    send_result = json.loads(runner.dispatch("tmux-a2a-postman", SEND, BODY))
    delivered = b""
    if scenario == "own-unread":
        popped = json.loads(runner.dispatch("tmux-a2a-postman", ["pop"]))
        delivered = runner.dispatch("cat", [popped["markdown_absolute_path"]])
    return {
        "pass": runner.complete(), "scenario": scenario,
        "send_self_state": send_result["self_state"],
        "delivered_archive_len": len(delivered),
        "delivered_archive_sha256": digest(delivered),
        "receipts": runner.receipts,
        "scope": "dispatcher-level only; no OS isolation or model selection proof",
    }


def main() -> int:
    import argparse
    import tempfile

    parser = argparse.ArgumentParser()
    parser.add_argument("--scenario", choices=["no-unread", "own-unread"], required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="offline-postman-") as name:
        result = scripted_probe(Path(name), args.scenario)
    print(json.dumps(result, sort_keys=True))
    return 0 if result["pass"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
