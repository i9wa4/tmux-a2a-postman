"""Deterministic acceptance/rejection tests for the offline transport fixture."""

import base64
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


FIXTURES = Path(__file__).resolve().parent
BODY = (FIXTURES / "body.md").read_bytes()
FAKE = FIXTURES / "tmux-a2a-postman"
GRADER = FIXTURES / "grade_trace.py"
CAT = FIXTURES / "cat"


class FakeTransportTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.trace = Path(self.directory.name) / "trace.jsonl"

    def invoke_fake(self, scenario, *args, body=b""):
        env = os.environ.copy()
        env.update(FAKE_POSTMAN_TRACE=str(self.trace), FAKE_POSTMAN_SCENARIO=scenario)
        return subprocess.run(
            [sys.executable, str(FAKE), *args], input=body, env=env,
            capture_output=True, check=False,
        )

    def grade(self, scenario):
        return subprocess.run(
            [sys.executable, str(GRADER), "--trace", str(self.trace),
             "--expected-body", str(FIXTURES / "body.md"), "--scenario", scenario],
            capture_output=True, text=True, check=False,
        )

    def read_archive(self, scenario):
        env = os.environ.copy()
        env.update(FAKE_POSTMAN_TRACE=str(self.trace), FAKE_POSTMAN_SCENARIO=scenario)
        archive = self.trace.with_suffix(".read.md")
        return subprocess.run(
            [sys.executable, str(CAT), str(archive)], env=env,
            capture_output=True, check=False,
        )

    def events(self):
        return [json.loads(line) for line in self.trace.read_text().splitlines()]

    def write_events(self, events):
        self.trace.write_text("".join(json.dumps(event) + "\n" for event in events))

    def test_no_unread_exact_send_passes(self):
        sent = self.invoke_fake("no-unread", "send-heredoc", "--to", "reviewer",
                                "--reply-required", body=BODY)
        self.assertEqual(sent.returncode, 0)
        self.assertEqual(json.loads(sent.stdout)["self_state"]["own_unread_count"], 0)
        self.assertEqual(base64.b64decode(self.events()[0]["stdin_base64"]), BODY)
        self.assertEqual(self.grade("no-unread").returncode, 0)

    def test_known_own_unread_requires_one_exact_pop(self):
        sent = self.invoke_fake("own-unread", "send-heredoc", "--to", "reviewer",
                                "--reply-required", body=BODY)
        self.assertEqual(json.loads(sent.stdout)["self_state"]["own_unread_count"], 1)
        popped = self.invoke_fake("own-unread", "pop")
        self.assertEqual(popped.returncode, 0)
        self.assertTrue(Path(json.loads(popped.stdout)["markdown_absolute_path"]).exists())
        read = self.read_archive("own-unread")
        self.assertEqual(read.returncode, 0)
        self.assertEqual(read.stdout, (FIXTURES / "archive.md").read_bytes())
        self.assertEqual(self.grade("own-unread").returncode, 0)

    def test_wrong_route_changed_bytes_duplicate_send_and_poll_fail(self):
        self.invoke_fake("no-unread", "send-heredoc", "--to", "reviewer",
                         "--reply-required", body=BODY)
        good = self.events()[0]
        bad_cases = [
            [{**good, "argv": ["send-heredoc", "--to", "other-node", "--reply-required"]}],
            [{**good, "stdin_base64": base64.b64encode(BODY.rstrip(b"\n")).decode()}],
            [good, good],
            [good, {"argv": ["get-status"], "stdin_base64": ""}],
            [good, {"argv": ["pop"], "stdin_base64": ""}],
        ]
        for events in bad_cases:
            with self.subTest(events=events):
                self.write_events(events)
                result = self.grade("no-unread")
                self.assertEqual(result.returncode, 1)
                self.assertFalse(json.loads(result.stdout)["pass"])

    def test_known_own_unread_rejects_missing_or_extra_pop_and_read(self):
        self.invoke_fake("own-unread", "send-heredoc", "--to", "reviewer",
                         "--reply-required", body=BODY)
        send = self.events()[0]
        self.invoke_fake("own-unread", "pop")
        self.read_archive("own-unread")
        good = self.events()
        for events in (
            [send], good[:2], [send, good[1], good[1], good[2]],
            [send, good[2]],
            [send, good[1], {**good[2], "stdout_base64": base64.b64encode(b"partial").decode()}],
            [send, good[1], {**good[2], "argv": ["cat", "/wrong/archive.md"]}],
            [send, good[1], {"argv": ["sleep", "1"]}, good[2]],
            [send, good[1], {"argv": ["/bin/cat", good[2]["argv"][1]]}],
        ):
            with self.subTest(events=events):
                self.write_events(events)
                result = self.grade("own-unread")
                self.assertEqual(result.returncode, 1)
                self.assertFalse(json.loads(result.stdout)["pass"])

    def test_malformed_empty_argv_returns_structured_failure(self):
        for event in ({"argv": []}, {"argv": "send-heredoc"}, []):
            with self.subTest(event=event):
                self.write_events([event])
                result = self.grade("no-unread")
                self.assertEqual(result.returncode, 1)
                self.assertFalse(json.loads(result.stdout)["pass"])
                self.assertIn("invalid argv", result.stdout)


if __name__ == "__main__":
    unittest.main()
