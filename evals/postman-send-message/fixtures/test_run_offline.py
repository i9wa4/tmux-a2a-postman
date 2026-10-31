"""No-model positive and bypass probes for the structured dispatcher."""

import base64
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from run_offline import ARCHIVE, BODY, SEND, DispatchDenied, OfflineDispatcher, digest, scripted_probe


class OfflineDispatcherTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)

    def runner(self, scenario="own-unread"):
        return OfflineDispatcher(self.directory, scenario)

    def sent(self, scenario="own-unread"):
        runner = self.runner(scenario)
        runner.dispatch("tmux-a2a-postman", SEND, BODY)
        return runner

    def test_both_scripted_branches_complete_with_runner_receipts(self):
        for scenario in ("no-unread", "own-unread"):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as name:
                result = scripted_probe(Path(name), scenario)
                self.assertTrue(result["pass"])
                self.assertEqual(result["receipts"][0]["argv"], SEND)
                self.assertEqual(result["receipts"][0]["stdin_base64"], base64.b64encode(BODY).decode())
                expected_count = 1 if scenario == "no-unread" else 3
                self.assertEqual(len(result["receipts"]), expected_count)
                if scenario == "own-unread":
                    read = result["receipts"][2]
                    self.assertTrue(read["delivered_after_eof"])
                    self.assertEqual(read["stdout_len"], len(ARCHIVE))
                    self.assertEqual(read["stdout_sha256"], digest(ARCHIVE))
                    self.assertEqual(result["delivered_archive_sha256"], digest(ARCHIVE))

    def test_malformed_send_response_is_terminal_after_execution(self):
        for altered in (b"{", b"[]", b'{"self_state":[]}'):
            with self.subTest(altered=altered), tempfile.TemporaryDirectory() as name:
                runner = OfflineDispatcher(Path(name), "own-unread")
                real_run = subprocess.run
                def replace_stdout(*args, **kwargs):
                    result = real_run(*args, **kwargs)
                    return subprocess.CompletedProcess(result.args, result.returncode, altered, result.stderr)
                with patch("run_offline.subprocess.run", side_effect=replace_stdout) as invoked:
                    with self.assertRaises(DispatchDenied):
                        runner.dispatch("tmux-a2a-postman", SEND, BODY)
                    self.assertEqual(invoked.call_count, 1)
                    with self.assertRaises(DispatchDenied):
                        runner.dispatch("tmux-a2a-postman", SEND, BODY)
                    self.assertEqual(invoked.call_count, 1)
                self.assertEqual(runner.phase, "denied")
                self.assertEqual(runner.receipts[0]["decision"], "executed")
                self.assertEqual(runner.receipts[1]["side_effects"], 1)
                self.assertEqual(runner.receipts[1]["prior_invocations"], 1)

    def test_failed_or_unknown_send_response_cannot_advance_or_retry(self):
        for scenario in ("no-unread", "own-unread"):
            count = 0 if scenario == "no-unread" else 1
            valid = {"sent": "fake-sent-001", "status": "queued",
                     "self_state": {"known": True, "own_unread_count": count}}
            for alteration in ("missing-sent", "false-sent", "empty-sent",
                               "wrong-status", "false-known", "missing-known"):
                with self.subTest(scenario=scenario, alteration=alteration), tempfile.TemporaryDirectory() as name:
                    reply = json.loads(json.dumps(valid))
                    if alteration == "missing-sent":
                        del reply["sent"]
                    elif alteration == "false-sent":
                        reply["sent"] = False
                    elif alteration == "empty-sent":
                        reply["sent"] = ""
                    elif alteration == "wrong-status":
                        reply["status"] = "failed"
                    elif alteration == "false-known":
                        reply["self_state"]["known"] = False
                    else:
                        del reply["self_state"]["known"]
                    altered = json.dumps(reply).encode("utf-8")
                    runner = OfflineDispatcher(Path(name), scenario)
                    real_run = subprocess.run
                    def replace_stdout(*args, **kwargs):
                        result = real_run(*args, **kwargs)
                        return subprocess.CompletedProcess(result.args, result.returncode, altered, result.stderr)
                    with patch("run_offline.subprocess.run", side_effect=replace_stdout) as invoked:
                        with self.assertRaises(DispatchDenied):
                            runner.dispatch("tmux-a2a-postman", SEND, BODY)
                        self.assertEqual(invoked.call_count, 1)
                        with self.assertRaises(DispatchDenied):
                            runner.dispatch("tmux-a2a-postman", ["pop"] if count else SEND, b"" if count else BODY)
                        self.assertEqual(invoked.call_count, 1)
                    self.assertEqual(runner.phase, "denied")
                    self.assertFalse(runner.complete())
                    self.assertEqual(runner.receipts[0]["decision"], "executed")
                    self.assertEqual(runner.receipts[1]["decision"], "denied")
                    self.assertEqual(runner.receipts[1]["side_effects"], 1)

    def test_malformed_or_unclaimed_pop_response_is_terminal_after_execution(self):
        for altered in (b"{", b"[]", b"{}", b'{"status":"empty"}',
                        b'{"status":"message"}', b'{"status":"message","message_id":"x"}'):
            with self.subTest(altered=altered), tempfile.TemporaryDirectory() as name:
                runner = OfflineDispatcher(Path(name), "own-unread")
                runner.dispatch("tmux-a2a-postman", SEND, BODY)
                real_run = subprocess.run
                def replace_stdout(*args, **kwargs):
                    result = real_run(*args, **kwargs)
                    return subprocess.CompletedProcess(result.args, result.returncode, altered, result.stderr)
                with patch("run_offline.subprocess.run", side_effect=replace_stdout) as invoked:
                    with self.assertRaises(DispatchDenied):
                        runner.dispatch("tmux-a2a-postman", ["pop"])
                    self.assertEqual(invoked.call_count, 1)
                    with self.assertRaises(DispatchDenied):
                        runner.dispatch("cat", [str(runner.archive_path)])
                    self.assertEqual(invoked.call_count, 1)
                self.assertEqual(runner.phase, "denied")
                self.assertEqual(runner.receipts[1]["decision"], "executed")
                self.assertEqual(runner.receipts[2]["side_effects"], 2)
                self.assertEqual(runner.receipts[2]["prior_invocations"], 2)

    def test_exact_send_route_flag_body_and_newline(self):
        variants = (
            ("tmux-a2a-postman", ["send-heredoc", "--to", "orchestrator", "--reply-required"], BODY),
            ("tmux-a2a-postman", ["send-heredoc", "--to", "reviewer"], BODY),
            ("tmux-a2a-postman", SEND + ["--json"], BODY),
            ("tmux-a2a-postman", SEND, BODY.rstrip(b"\n")),
            ("tmux-a2a-postman", SEND, BODY + b"\n"),
            ("/opt/homebrew/bin/tmux-a2a-postman", SEND, BODY),
            ("./tmux-a2a-postman", SEND, BODY),
            ("sh", ["-c", "tmux-a2a-postman send-heredoc"], BODY),
        )
        for executable, argv, body in variants:
            with self.subTest(executable=executable, argv=argv, body=body):
                runner = self.runner()
                with self.assertRaises(DispatchDenied):
                    runner.dispatch(executable, argv, body)
                self.assertFalse(runner.fake_trace.exists())
                self.assertEqual(runner.receipts[-1]["side_effects"], 0)

    def test_poll_sleep_alternate_reader_and_post_denial_fallback(self):
        for executable, argv in (
            ("tmux-a2a-postman", ["get-status"]),
            ("tmux-a2a-postman", ["pop", "--json"]),
            ("sleep", ["1"]),
            ("/bin/cat", ["any"]),
            ("python3", ["-c", "print(1)"]),
        ):
            with self.subTest(executable=executable, argv=argv), tempfile.TemporaryDirectory() as name:
                runner = OfflineDispatcher(Path(name), "own-unread")
                runner.dispatch("tmux-a2a-postman", SEND, BODY)
                trace_before = runner.fake_trace.read_bytes()
                with self.assertRaises(DispatchDenied):
                    runner.dispatch(executable, argv)
                with self.assertRaises(DispatchDenied):
                    runner.dispatch("tmux-a2a-postman", ["pop"])
                self.assertEqual(runner.fake_trace.read_bytes(), trace_before)
                self.assertFalse(runner.archive_path.exists())
                self.assertFalse(runner.complete())

    def test_duplicate_send_missing_or_extra_pop_and_branch_mismatch(self):
        for scenario in ("no-unread", "own-unread"):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as name:
                runner = OfflineDispatcher(Path(name), scenario)
                runner.dispatch("tmux-a2a-postman", SEND, BODY)
                with self.assertRaises(DispatchDenied):
                    runner.dispatch("tmux-a2a-postman", SEND, BODY)
                self.assertFalse(runner.complete())
        runner = self.sent("no-unread")
        with self.assertRaises(DispatchDenied):
            runner.dispatch("tmux-a2a-postman", ["pop"])
        self.assertFalse(runner.archive_path.exists())
        with tempfile.TemporaryDirectory() as name:
            runner = OfflineDispatcher(Path(name), "own-unread")
            runner.dispatch("tmux-a2a-postman", SEND, BODY)
            self.assertFalse(runner.complete())
            runner.dispatch("tmux-a2a-postman", ["pop"])
            self.assertFalse(runner.complete())
            with self.assertRaises(DispatchDenied):
                runner.dispatch("tmux-a2a-postman", ["pop"])

    def test_archive_path_and_bytes_are_bound_to_immediate_pop(self):
        for mutate in ("partial", "symlink", "wrong-path"):
            with self.subTest(mutate=mutate), tempfile.TemporaryDirectory() as name:
                runner = OfflineDispatcher(Path(name), "own-unread")
                runner.dispatch("tmux-a2a-postman", SEND, BODY)
                popped = json.loads(runner.dispatch("tmux-a2a-postman", ["pop"]))
                path = popped["markdown_absolute_path"]
                if mutate == "partial":
                    runner.archive_path.write_bytes(ARCHIVE[:5])
                elif mutate == "symlink":
                    other = Path(name) / "other.md"
                    other.write_bytes(ARCHIVE)
                    runner.archive_path.unlink()
                    runner.archive_path.symlink_to(other)
                else:
                    path = str(Path(name) / "other.md")
                trace_before = runner.fake_trace.read_bytes()
                with self.assertRaises(DispatchDenied):
                    runner.dispatch("cat", [path])
                self.assertEqual(runner.fake_trace.read_bytes(), trace_before)
                self.assertFalse(runner.complete())

    def test_forged_fake_trace_does_not_change_runner_receipts(self):
        runner = self.sent("no-unread")
        receipts_before = list(runner.receipts)
        runner.fake_trace.write_text('{"argv":["pop"]}\n', encoding="utf-8")
        self.assertEqual(runner.receipts, receipts_before)
        self.assertEqual(runner.receipts[0]["argv"], SEND)


if __name__ == "__main__":
    unittest.main()
