"""Check one authored classification case; never access a session or model."""

import copy
import json
from pathlib import Path


CASE_PATH = Path(__file__).with_name("saved-workflow-case.json")
ORIGINAL = (
    "Draft a migration guide, verify its commands on macOS and Linux, "
    "and obtain my signoff before publication."
)
REQUIREMENTS = [
    ("C1", "T0 original", "migration guide", "PASS", "T5 guide exists"),
    ("C2", "T0 original", "macOS and Linux command checks", "OPEN",
     "T5 macOS check only; Linux check absent"),
    ("C3", "T0 original", "human signoff before publication", "UNKNOWN",
     "T5 no human signoff record"),
    ("C4", "T4 human amendment", "rollback note", "PASS",
     "T5 rollback note exists"),
]
EVENTS = [
    ("T0", "human_request", "C1 guide; C2 macOS and Linux command checks; "
     "C3 human signoff before publication"),
    ("T1", "plan", "guide and macOS check planned; Linux check omitted"),
    ("T2", "format_review", "no new issue"),
    ("T3", "format_review", "no new issue"),
    ("T4", "human_correction", "retain C2 macOS and Linux checks; "
     "add C4 rollback note"),
    ("T5", "final_artifact", "guide and rollback note exist; macOS check "
     "passes; Linux check and human signoff absent"),
]
EXPECTED_COMPLEXITY = {
    "baseline": {
        "code": "0 product LOC added; proposed config reminder only; time and maintenance UNKNOWN",
        "config": "3 existing handoff paragraphs plus 1 proposed reminder; 2 duplicate rules and 1 obsolete route; time and maintenance UNKNOWN",
        "Help": "0 new Help pointers; existing lookup burden unmeasured; time and maintenance UNKNOWN",
        "Skill": "0 evaluator Skills or references; time and maintenance UNKNOWN",
        "fixture": "0 evaluator cases, checkers, trigger suites or trigger tasks; time and maintenance UNKNOWN",
        "operation": "3 instruction lookups; time and maintenance UNKNOWN"
    },
    "candidate": {
        "code": "0 product LOC added; checker counted under fixture; time and maintenance UNKNOWN",
        "config": "1 sentence and 1 Help pointer; 0 duplicate rules and 0 obsolete routes; time and maintenance UNKNOWN",
        "Help": "1 canonical Help pointer; time and maintenance UNKNOWN",
        "Skill": "1 thin evaluator Skill and 2 references; time and maintenance UNKNOWN",
        "fixture": "1 JSON case, 1 checker, 1 trigger suite and 3 trigger tasks; time and maintenance UNKNOWN",
        "operation": "1 route lookup; time and maintenance UNKNOWN"
    }
}
DECISIONS = {
    "config": "CONFIG_SUBTRACTION: delete obsolete route, merge duplicate POP-ID rules, "
              "point to Help; retain actual POP ID and native route",
    "product": "REPRODUCE_BEFORE_ASSIGNMENT: request-ID mismatch is hypothetical; "
               "test exact binary and contract, assign developer only if reproduced",
    "unsafe_replay": "EFFICACY_UNKNOWN: if faithful replay is unsafe or unavailable, "
                     "seek separate consent for bounded check",
}


def require(condition, reason):
    if not condition:
        raise ValueError(reason)


def unique_fields(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON field: " + key)
        result[key] = value
    return result


def validate(case):
    require(case.get("kind") == "SYNTHETIC_PUBLIC_SAFE_REPORT_CLASSIFICATION",
            "case kind")
    require(case.get("session") == "demo-migration" and
            case.get("workspace") == "sample/project", "case scope")
    require(case.get("original_request") == ORIGINAL, "original request changed")

    record = case.get("record", {})
    require(record.get("system") == "Logbook" and
            record.get("canonical_id") == "sample/project/demo-migration",
            "canonical Logbook identity")
    require(record.get("checkpoint") == "T5" and
            record.get("previous_checkpoint") == "T4",
            "stale or discontinuous checkpoint")
    require(record.get("duplicate_ids") == [], "duplicate canonical record")

    criteria = case.get("criteria", [])
    require(len(criteria) == len(REQUIREMENTS), "criterion count")
    for item, expected in zip(criteria, REQUIREMENTS):
        require(tuple(item.get(key) for key in
                      ("id", "origin", "requirement", "status", "evidence")) == expected,
                "criterion requirement or evidence changed: " + expected[0])

    events = case.get("timeline", [])
    require(len(events) == len(EVENTS), "timeline count")
    for item, expected in zip(events, EVENTS):
        require(tuple(item.get(key) for key in ("id", "kind", "result")) == expected,
                "timeline event changed: " + expected[0])
    require(events[2].get("artifact_digest") ==
            events[3].get("artifact_digest") == "same-digest",
            "repeated review digest evidence")
    require(record.get("event_ids") == [event[0] for event in EVENTS] and
            record["checkpoint"] == events[-1]["id"] and
            record["previous_checkpoint"] == events[-2]["id"],
            "canonical checkpoint continuity")

    report = case.get("candidate_report", {})
    fields = {
        "status", "original_verbatim_request", "record_id", "checkpoint",
        "first_divergence", "missed_requirements", "human_corrections",
        "unnecessary_repeats", "human_acceptance", "existing_rule",
        "duplicate_must", "owner", "time_resource_burden", "behavioral_efficacy",
        "total_complexity", "decisions",
    }
    require(set(report) == fields, "report field contract")
    require(report["record_id"] == record["canonical_id"] and
            report["checkpoint"] == record["checkpoint"],
            "report record continuity")
    require(report["status"] == "BLOCKED" and
            [item["status"] for item in criteria] ==
            ["PASS", "OPEN", "UNKNOWN", "PASS"], "false task completion")
    require(report["original_verbatim_request"] == "AUTHORED SYNTHETIC ONLY",
            "invented original-request provenance")
    require(report["first_divergence"] == "T1 omitted Linux from C2" and
            "Linux check omitted" in events[1]["result"], "first divergence")
    require(report["missed_requirements"] == ["C2", "C3"],
            "omitted original criterion")
    require(report["human_corrections"] == 1 and
            events[4]["kind"] == "human_correction", "human correction count")
    require(report["unnecessary_repeats"] ==
            "one repeated format review; avoidability beyond identical digest UNKNOWN",
            "repeat evidence limit")
    require(report["human_acceptance"] == "UNKNOWN" and
            report["behavioral_efficacy"] == "UNKNOWN" and
            report["time_resource_burden"] == "UNKNOWN",
            "unsupported acceptance or efficacy")
    require(report["existing_rule"] == "config/postman.md:99-111" and
            report["duplicate_must"] == "reject", "existing rule or duplicate MUST")
    require(report["owner"] == "task planner and reporter; cause of omission UNKNOWN",
            "wrong owner or invented cause")

    complexity = report["total_complexity"]
    require(set(complexity) == {"baseline", "candidate"}, "complexity comparison")
    for side in ("baseline", "candidate"):
        require(set(complexity[side]) == set(EXPECTED_COMPLEXITY[side]),
                "complexity dimensions: " + side)
        for dimension, expected in EXPECTED_COMPLEXITY[side].items():
            require(complexity[side][dimension] == expected,
                    "known complexity fact contradicted: " + side + "/" + dimension)
    require(report["decisions"] == DECISIONS, "missing or changed decision branch")

    baseline = case.get("baseline_report", {})
    require(baseline == {"status": "PASS", "human_acceptance": "PASS",
                         "duplicate_must": "add checklist reminder"},
            "invented baseline negative control changed")


def negative(base, label, mutate, expected_reason):
    altered = copy.deepcopy(base)
    mutate(altered)
    try:
        validate(altered)
    except ValueError as exc:
        require(expected_reason in str(exc),
                "negative reason mismatch: " + label + ": " + str(exc))
        print("NEGATIVE_PASS: " + label + " -> " + str(exc))
        return
    raise ValueError("negative control accepted: " + label)


def run():
    case = json.loads(CASE_PATH.read_text(), object_pairs_hook=unique_fields)
    validate(case)
    negative(case, "stale record", lambda c: c["record"].update(checkpoint="T4"),
             "stale or discontinuous checkpoint")
    negative(case, "duplicate record", lambda c: c["record"]["duplicate_ids"].append(
        "sample/project/demo-migration-copy"), "duplicate canonical record")
    negative(case, "missing decision branch", lambda c: c["candidate_report"]
             ["decisions"].pop("unsafe_replay"), "missing or changed decision branch")
    negative(case, "changed requirement", lambda c: c["criteria"][1].update(
        requirement="macOS check only"), "criterion requirement or evidence changed: C2")
    negative(case, "contradictory evidence", lambda c: c["criteria"][1].update(
        evidence="T5 Linux check passes"), "criterion requirement or evidence changed: C2")
    negative(case, "wrong owner", lambda c: c["candidate_report"].update(
        owner="product developer"), "wrong owner or invented cause")
    negative(case, "unsupported efficacy", lambda c: c["candidate_report"].update(
        behavioral_efficacy="PASS"), "unsupported acceptance or efficacy")
    negative(case, "false PASS", lambda c: c["candidate_report"].update(status="PASS"),
             "false task completion")
    negative(case, "invented human acceptance", lambda c: c["candidate_report"].update(
        human_acceptance="PASS"), "unsupported acceptance or efficacy")
    negative(case, "duplicate MUST", lambda c: c["candidate_report"].update(
        duplicate_must="add checklist reminder"), "existing rule or duplicate MUST")
    negative(case, "omitted original criterion", lambda c: c["candidate_report"].update(
        missed_requirements=["C3"]), "omitted original criterion")
    negative(case, "contradictory config cost", lambda c: c["candidate_report"]
             ["total_complexity"]["candidate"].update(
                 config="one thousand new duplicate rules; time and maintenance UNKNOWN"),
             "known complexity fact contradicted: candidate/config")
    negative(case, "contradictory product LOC", lambda c: c["candidate_report"]
             ["total_complexity"]["candidate"].update(
                 code="adds 3000 product LOC; time and maintenance UNKNOWN"),
             "known complexity fact contradicted: candidate/code")
    negative(case, "omitted trigger inventory", lambda c: c["candidate_report"]
             ["total_complexity"]["candidate"].update(
                 fixture="1 JSON case and 1 checker; time and maintenance UNKNOWN"),
             "known complexity fact contradicted: candidate/fixture")
    print("AUTHOR_CASE_CLASSIFICATION_PASS; EMPIRICAL_AGENT_BEHAVIOR_UNKNOWN")


if __name__ == "__main__":
    run()
