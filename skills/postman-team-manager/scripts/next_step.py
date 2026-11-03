"""Pure advisory policy example; inputs are assessed facts, not CLI fields.

No I/O, ownership inference, scheduling, sending, or artifact verification.
The manager remains responsible for supporting every fact with evidence.
"""


def next_step(*, direct_report, objective_known, criteria_verified=False,
              finding="unknown", inquiry_pending=False, evidence_changed=False,
              direction_problem=False):
    flags = [direct_report, objective_known, criteria_verified, inquiry_pending,
             evidence_changed, direction_problem]
    if any(type(value) is not bool for value in flags):
        raise ValueError("assessed flags must be explicit booleans")
    if finding not in {"unknown", "working", "blocked", "finished_unreported", "reply_received"}:
        raise ValueError("unknown assessed finding")
    if not direct_report:
        return "report_scope_limitation"
    if not objective_known:
        return "report_missing_objective"
    if criteria_verified:
        return "report_complete_and_stop_work"
    if inquiry_pending and not evidence_changed:
        return "report_unresolved_inquiry_and_escalate"
    if direction_problem:
        return "request_smallest_in_scope_next_step"
    return {
        "working": "report_active_work_and_milestone",
        "blocked": "report_blocker_and_responsible_party",
        "finished_unreported": "request_existing_result_report",
        "reply_received": "verify_reply_evidence_against_criteria",
        "unknown": "investigate_then_one_targeted_inquiry",
    }[finding]
