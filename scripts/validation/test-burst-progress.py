"""A durable claim/decision is progress before its terminal tool returns."""

from pathlib import Path
import tempfile
import unittest

from burst_progress import approval_progress


class BurstProgressTests(unittest.TestCase):
    def test_durable_progress_precedes_final_proof_and_command_completion(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state, evidence = root / "state", root / "evidence"
            evidence.mkdir()
            self.assertEqual(approval_progress(evidence, state, 0), (0, 0, 0, 0))
            self.assertFalse(state.exists(), "progress observation initialized state")
            read = state / "work/agents/approver/read"
            sent = read.parent / "sent"
            read.mkdir(parents=True)
            sent.mkdir()
            (read / "m-first.md").touch()
            self.assertEqual(approval_progress(evidence, state, 0), (0, 0, 0, 0))
            (read / "m-first.pop.json").touch()
            self.assertEqual(approval_progress(evidence, state, 0), (1, 0, 0, 0))
            (sent / "m-decision.md").touch()
            self.assertEqual(approval_progress(evidence, state, 0), (1, 1, 0, 0))
            (evidence / "approver.claim.first.json").touch()
            self.assertEqual(approval_progress(evidence, state, 1), (1, 1, 1, 1))

    def test_inbox_and_other_agents_do_not_count_as_approver_progress(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory) / "state"
            for tail in ["work/agents/approver/inbox/m-new.md", "work/agents/worker/read/m-old.pop.json"]:
                path = state / tail
                path.parent.mkdir(parents=True, exist_ok=True)
                path.touch()
            self.assertEqual(approval_progress(Path(directory) / "evidence", state, 0), (0, 0, 0, 0))


if __name__ == "__main__":
    unittest.main()
