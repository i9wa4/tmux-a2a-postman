"""Deterministic data scenarios for the recall skill's executable reference."""

import copy
import os
from pathlib import Path
import runpy
import unittest

ASSETS = Path(os.environ.get("POSTMAN_SKILLS_ROOT", Path(__file__).resolve().parents[2] / "help"))
REFERENCE = runpy.run_path(str(ASSETS / "postman-recall/scripts/select_day.py"))
SELECT = REFERENCE["latest_day"]
INSTANT = REFERENCE["recorded_instant"]
OWN = "/home/example/.local/state/tmux-a2a-postman/work/agents/worker"


def record(number, box, timestamp, **extra):
    identifier = "m-" + f"{number:032x}"
    return {"id": identifier, "box": box, "created_at": timestamp,
            "path": OWN + "/" + box + "/" + identifier + ".md",
            "from": "worker" if box == "sent" else "peer",
            "to": "worker" if box == "read" else "peer", **extra}


class RecallDataTests(unittest.TestCase):
    def test_friday_is_recalled_on_monday_without_a_clock_or_mtime_window(self):
        # Simulated restart: Monday 2026-11-02. Source metadata is actual
        # Message.created_at, not a daemon date or a POP receipt timestamp.
        rows = [record(1, "sent", "2026-10-29T18:00:00Z"),
                record(2, "read", "2026-10-30T10:00:00Z", mtime="2026-11-02T10:00:00Z"),
                record(3, "sent", "2026-10-30T17:00:00Z", claimed_at="2026-11-02T11:00:00Z")]
        got = SELECT(rows, OWN)
        self.assertEqual(got["date"], "2026-10-30")
        self.assertEqual([row["id"] for row in got["messages"]], [rows[1]["id"], rows[2]["id"]])
        self.assertEqual((got["read_count"], got["sent_count"]), (1, 1))

    def test_newest_may_be_in_either_directory(self):
        for newest_box in ["read", "sent"]:
            with self.subTest(newest_box=newest_box):
                old_box = "sent" if newest_box == "read" else "read"
                rows = [record(1, old_box, "2026-12-31T23:59:59.999999999Z"),
                        record(2, newest_box, "2027-01-01T00:00:00Z")]
                got = SELECT(rows, OWN)
                self.assertEqual(got["date"], "2027-01-01")
                self.assertEqual(got["messages"], rows[1:])

    def test_mixed_chronology_is_sorted_across_directions(self):
        rows = [record(3, "sent", "2026-11-01T14:00:00Z"),
                record(1, "sent", "2026-11-01T09:00:00Z"),
                record(2, "read", "2026-11-01T12:00:00Z")]
        self.assertEqual([row["id"] for row in SELECT(rows, OWN)["messages"]],
                         [rows[1]["id"], rows[2]["id"], rows[0]["id"]])

    def test_timezone_and_year_boundaries_use_utc_date(self):
        rows = [record(1, "read", "2027-01-01T00:30:00+02:00"),
                record(2, "sent", "2026-12-31T23:00:00Z")]
        got = SELECT(rows, OWN)
        self.assertEqual(got["date"], "2026-12-31")
        self.assertEqual(got["messages"], rows)
        self.assertEqual(INSTANT("2026-11-01T23:30:00-02:00")[1], "2026-11-02")

    def test_nanoseconds_are_not_truncated_to_microseconds(self):
        earlier = record(9, "read", "2026-11-01T10:00:00.123456788Z")
        later = record(1, "sent", "2026-11-01T10:00:00.123456789Z")
        self.assertEqual(SELECT([later, earlier], OWN)["messages"], [earlier, later])

    def test_equal_instants_tie_by_id_then_direction(self):
        read = record(1, "read", "2026-11-01T12:00:00.000000001+02:00")
        sent = record(1, "sent", "2026-11-01T10:00:00.000000001Z")
        other = record(2, "sent", "2026-11-01T10:00:00.000000001Z")
        self.assertEqual(SELECT([other, sent, read], OWN)["messages"], [read, sent, other])

    def test_missing_or_invalid_timestamp_cannot_silently_choose_another_day(self):
        for value in [None, "", "yesterday", "2026-11-01", "2026-11-01T10:00:00",
                      "2026-02-30T10:00:00Z", "2026-11-01T10:00:00.1234567890Z",
                      "2026-11-01T10:00:00+24:00", "2026-11-01T10:00:00+00:60"]:
            with self.subTest(timestamp=value), self.assertRaises(ValueError):
                SELECT([record(1, "read", "2026-11-01T10:00:00Z"), record(2, "sent", value)], OWN)

    def test_empty_existing_inventory_has_no_invented_day(self):
        self.assertEqual(SELECT([], OWN), {"date": None, "read_count": 0, "sent_count": 0, "messages": []})

    def test_inbox_receipts_and_other_history_paths_are_not_selectable(self):
        for box, path in [("inbox", None), ("draft", None), ("failed", None),
                          ("read", "/home/example/other/agents/worker/read/m-" + "0" * 31 + "1.md"),
                          ("read", OWN + "/read/m-" + "0" * 31 + "1.pop.json")]:
            with self.subTest(box=box, path=path), self.assertRaises(ValueError):
                row = record(1, box, "2026-11-01T10:00:00Z")
                if path is not None:
                    row["path"] = path
                SELECT([row], OWN)

    def test_wrong_participant_id_and_duplicate_path_are_rejected(self):
        for edit in [{"to": "other"}, {"id": "wrong-id"}, {"path": OWN + "/read/../inbox/hidden.md"}]:
            with self.subTest(edit=edit), self.assertRaises(ValueError):
                SELECT([record(1, "read", "2026-11-01T10:00:00Z", **edit)], OWN)
        row = record(1, "sent", "2026-11-01T10:00:00Z")
        with self.assertRaises(ValueError):
            SELECT([row, row], OWN)

    def test_identical_cross_direction_copies_keep_both_counts_but_conflicts_fail(self):
        rows = [record(1, "read", "2026-11-01T10:00:00Z", **{"from": "worker"}, archive_sha256="a" * 64),
                record(1, "sent", "2026-11-01T10:00:00Z", to="worker", archive_sha256="a" * 64)]
        got = SELECT(rows, OWN)
        self.assertEqual((got["read_count"], got["sent_count"]), (1, 1))
        rows[1]["archive_sha256"] = "b" * 64
        with self.assertRaises(ValueError):
            SELECT(rows, OWN)

    def test_older_exact_reference_is_retained_without_implicit_full_history_expansion(self):
        original = record(1, "sent", "2026-10-29T10:00:00Z", input_request_id="ireq_" + "0" * 31 + "1")
        reply = record(2, "read", "2026-10-30T10:00:00Z", reply_to=original["id"],
                       replies=[{"message_id": original["id"], "input_request_id": original["input_request_id"]}])
        self.assertEqual(SELECT([original, reply], OWN)["messages"], [reply])
        self.assertEqual(reply["replies"][0]["message_id"], original["id"])

    def test_selection_does_not_modify_inputs_and_requires_an_absolute_scope(self):
        rows = [record(2, "sent", "2026-10-30T10:00:00Z"), record(1, "read", "2026-10-30T09:00:00Z")]
        before = copy.deepcopy(rows)
        SELECT(rows, OWN)
        self.assertEqual(rows, before)
        with self.assertRaises(ValueError):
            SELECT(rows, "agents/worker")


if __name__ == "__main__":
    unittest.main()
