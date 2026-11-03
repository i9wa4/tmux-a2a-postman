"""Deterministic reader checks including interrupted/truncated histories."""

import copy
import json
import os
from pathlib import Path
import runpy
import tempfile
import unittest

SOURCE = Path(os.environ.get("POSTMAN_DIAGNOSTIC_ANALYZER", Path(__file__).resolve().parents[2] / "skills/postman-usage/scripts/analyze-invocations.py"))
REFERENCE = runpy.run_path(str(SOURCE))
READ = REFERENCE["read_invocation"]
SUM = REFERENCE["summarize"]


class AnalysisTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / "inv-example.jsonl"
        self.events = [
            {"schema_version": 1, "invocation_id": "inv-example", "seq": 0, "event": "start", "identity": {"node": "builder"}},
            {"schema_version": 1, "invocation_id": "inv-example", "seq": 1, "event": "phase_start", "phase": "approval_wait"},
            {"schema_version": 1, "invocation_id": "inv-example", "seq": 2, "event": "end", "outcome": "failed", "duration_ms": 12},
        ]

    def write(self, events):
        self.path.write_text("".join(json.dumps(e) + "\n" for e in events))

    def test_failed_and_incomplete_samples_are_visible_without_repair(self):
        self.write(self.events)
        failed = READ(self.path)
        self.assertFalse(failed["incomplete"])
        with self.path.open("ab") as stream:
            stream.write(b'{"event":')
        before = self.path.read_bytes()
        partial = READ(self.path)
        self.assertTrue(partial["incomplete"])
        report = SUM([failed, partial])
        self.assertEqual(report["outcomes"], {"failed": 1, "incomplete": 1})
        self.assertEqual(report["median_ms"], 12)
        self.assertEqual(report["incomplete_files"][0]["last_phase"], "approval_wait")
        self.assertEqual(self.path.read_bytes(), before)

    def test_phase_start_without_end_is_incomplete_not_a_retry_grant(self):
        self.write(self.events[:2])
        self.assertTrue(READ(self.path)["incomplete"])

    def test_invalid_sequence_identity_and_schema_are_incomplete(self):
        for edit in [{"seq": 10}, {"invocation_id": "different"}, {"schema_version": 2}, {"duration_ms": -1}, {"duration_ms": float("nan")}, {"outcome": []}]:
            with self.subTest(edit=edit):
                events = copy.deepcopy(self.events)
                events[-1].update(edit)
                self.write(events)
                self.assertTrue(READ(self.path)["incomplete"])

    def test_corrupt_identity_does_not_crash_or_claim_completion(self):
        self.events[0]["identity"] = []
        self.write(self.events)
        self.assertTrue(READ(self.path)["incomplete"])

    def test_missing_and_oversized_files_stay_unknown(self):
        self.assertTrue(READ(self.path)["incomplete"])
        self.path.write_bytes(b"x" * (128 * 1024 + 1))
        self.assertEqual(READ(self.path)["reason"], "oversized")


if __name__ == "__main__":
    unittest.main()
