"""Offline reference-policy scenarios, not model compliance evaluations."""

import os
from pathlib import Path
import runpy
import unittest

ASSETS = Path(os.environ.get("POSTMAN_HELP_ASSET_ROOT", Path(__file__).resolve().parents[2] / "internal/helpdocs/assets"))
DECIDE = runpy.run_path(str(ASSETS / "team-manager/next_step.py"))["next_step"]


def decide(**facts):
    return DECIDE(**{"direct_report": True, "objective_known": True, **facts})


class ManagementScenarios(unittest.TestCase):
    def test_outstanding_specific_request_needs_investigation(self):
        self.assertEqual(decide(), "investigate_then_one_targeted_inquiry")

    def test_inquiry_without_response_is_not_recovery_or_another_ping(self):
        self.assertEqual(decide(inquiry_pending=True), "report_unresolved_inquiry_and_escalate")

    def test_stale_or_null_capture_never_proves_a_task_state(self):
        # Saved screen evidence supplies no assessed finding; it cannot
        # turn unknown into working, blocked or finished.
        for observation in [None, "2020-01-01T00:00:00Z"]:
            with self.subTest(last_capture_at=observation):
                self.assertEqual(decide(), "investigate_then_one_targeted_inquiry")

    def test_genuine_reply_requires_task_evidence_verification(self):
        self.assertEqual(decide(finding="reply_received", inquiry_pending=True, evidence_changed=True),
                         "verify_reply_evidence_against_criteria")

    def test_unchanged_repeated_checks_cannot_resend(self):
        actions = [decide(inquiry_pending=True) for _ in range(10)]
        self.assertEqual(set(actions), {"report_unresolved_inquiry_and_escalate"})

    def test_long_running_work_is_not_a_stall(self):
        self.assertEqual(decide(finding="working", inquiry_pending=True, evidence_changed=True),
                         "report_active_work_and_milestone")

    def test_completed_but_unreported_does_not_duplicate_execution(self):
        self.assertEqual(decide(finding="finished_unreported"), "request_existing_result_report")

    def test_blocker_needs_owner_and_smallest_prerequisite(self):
        self.assertEqual(decide(finding="blocked"), "report_blocker_and_responsible_party")

    def test_scope_growth_or_repeated_pointless_checks_need_minimal_step(self):
        for finding in ["working", "unknown", "reply_received"]:
            self.assertEqual(decide(finding=finding, direction_problem=True),
                             "request_smallest_in_scope_next_step")

    def test_sufficient_criteria_stop_even_when_there_are_more_possible_checks(self):
        self.assertEqual(decide(criteria_verified=True, direction_problem=True, inquiry_pending=True),
                         "report_complete_and_stop_work")

    def test_missing_objective_or_direct_scope_is_not_inferred(self):
        self.assertEqual(decide(objective_known=False, criteria_verified=True), "report_missing_objective")
        self.assertEqual(decide(direct_report=False, criteria_verified=True), "report_scope_limitation")

    def test_mistyped_assessments_cannot_be_truthy_authority(self):
        for values in [{"direct_report": "builder"}, {"criteria_verified": "false"},
                       {"inquiry_pending": None}, {"finding": "idle"}]:
            with self.subTest(values=values), self.assertRaises(ValueError):
                decide(**values)


if __name__ == "__main__":
    unittest.main()
