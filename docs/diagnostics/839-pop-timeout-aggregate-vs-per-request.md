# #839: aggregate daemon metrics vs. per-request pop-timeout outcomes

Bounded diagnostic artifact, correlating `get-status --debug` daemon-wide
aggregate fields against individual request-level `inspect-daemon-submit`
outcomes for requests whose client-side `pop` call hit the 30s timeout.

This is a correlation record only. It does not claim a root cause for the
timeouts or for the daemon-wide counters; it exists to make the
relationship between the two observation layers (aggregate snapshot vs.
per-request resolution) inspectable side by side.

## 1. Why aggregate counters cannot identify a single request

`get-status --debug` reports daemon-wide counters scoped to the whole
daemon process, not to one session or one request:

- `pending_request_count`, `late_response_count`: counts across all
  in-flight/late daemon-submit requests at snapshot time.
- `oldest_late_response_age_seconds`, `oldest_pending_age_seconds`: the
  single oldest age in each bucket, daemon-wide.
- `active_worker_count`, `worker_limit`: `worker_limit` is documented as a
  per-session bound; the aggregate `active_worker_count` does not indicate
  how many of those workers belong to any one session.

None of these fields carry a request ID, a session ID, or a per-request
timestamp, so an aggregate snapshot cannot, by itself, confirm which
specific request(s) contributed to a given counter value, or when any one
of them actually committed.

## 2. Aggregate snapshot (from #839, reproduced for reference)

```text
observed_at: 2026-09-30T14:32:26.388917Z
active_worker_count: 5
pending_request_count: 6
late_response_count: 47
oldest_late_response_age_seconds: 3350
oldest_pending_age_seconds: 209
saturation_count: 0
worker_limit: 8
```

## 3. Per-request outcomes (>=3), correlated against the window above

Each row is independently resolvable via `inspect-daemon-submit --id
<request-id>`, which returns a request-scoped `storage_state`/outcome, as
opposed to the daemon-wide aggregate above.

| Request ID                       | Created (UTC)        | Client-side `pop` behavior | `inspect-daemon-submit` outcome | `handled_at` / resolved age                             |
| -------------------------------- | -------------------- | -------------------------- | ------------------------------- | ------------------------------------------------------- |
| `20260930-145217-reb37`          | 2026-09-30T14:52:17Z | 30s client timeout         | `late_response`                 | `handled_at` 2026-09-30T14:52:50Z (~33s after creation) |
| `20260930-144001-rcff7`          | 2026-09-30T14:40:01Z | 30s client timeout         | `claimed`                       | inspected ~36s after creation                           |
| (third example — see Gaps below) |                      | 30s client timeout         | not yet independently captured  | not yet independently captured                          |

**Gaps, stated explicitly:**

- Only two independently-resolved per-request examples were available in

  #839's own body text at the time this artifact was written. A third

  per-request example was not captured in the issue, and this artifact
  does not fabricate one. Per the task's explicit constraint, no live
  `pop` was run against the active fleet to manufacture a third sample;
  obtaining a genuine third example requires a future bounded, isolated
  reproduction (e.g. the `#563` soak-validation runbook), not an ad hoc
  probe against this session's own live daemon.
- **Read-only search attempt (this rework):** searched this session's own
  daemon journal records
  (`~/.local/state/tmux-a2a-postman/*/tmux-a2a-postman/journal/records/
  *.json`) for any independently-resolved request matching the `#839`
  incident window (2026-09-30). No matches were found: this session's
  journal history only covers activity from 2026-10-03 onward, after the
  incident window #839 describes. This criterion (a genuine third
  independently-resolved per-request example) is therefore explicitly
  left **unresolved** rather than fabricated or obtained via a live `pop`
  probe against the active fleet, which was out of scope for this task.
- The two available examples themselves resolved to *different* outcomes
  (`late_response` vs. `claimed`), which is itself worth recording as a
  correlation finding: the same client-observable symptom (30s timeout)
  does not map to one single server-side resolution state.
- The aggregate snapshot's `observed_at` (14:32:26Z) predates both
  request examples (14:40:01Z and 14:52:17Z); the snapshot and the two
  request rows are from the same general incident window described in

  #839, but are not a single atomically-captured correlated triple. This

  artifact does not claim otherwise.
- `oldest_pending_age_seconds: 209` at the aggregate snapshot's timestamp
  exceeds the 30s queue-age threshold used by the `#563` soak-validation
  runbook (`docs/design/daemon-soak-validation.md:58-61,69`); this
  artifact records that numeric relationship only, and does not claim
  that the pending request measured by that counter is either of the two
  per-request rows above (no request ID is attached to the aggregate
  counter).

## 4. Non-claims (explicit)

- No claim is made about worker-pool sizing, scheduling fairness across
  sessions, or any other causal mechanism for the timeouts.
- No claim is made that this correlation is a `#676` regression.
- No claim is made that this is a formal `#563` soak-validation result;
  it is a bounded, point-in-time correlation record pending a real soak
  run.

## 5. Source

All data points are reproduced from issue #839's own body text (filed
observations), cross-referenced against `docs/design/daemon-soak-
validation.md` and `scripts/validation/daemon_soak_check.go` for the
soak-threshold context cited above. No new live-fleet probing was
performed to produce this artifact.
