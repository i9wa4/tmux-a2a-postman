# Issue #621: Convention Meter + Escalation Push

## 1. Objective

Implement the D4 convention meter projection and daemon threshold-push
escalation path.

## 2. Original Checklist

- [x] New projection computes per-node violation rate for missing `verdictOf`
  on verdict replies, missing evidence on completion claims, and missing
  reply references.
- [x] Violation rates are exposed in `get-status` / `session-status` output as
  machine-readable per-node figures.
- [x] Daemon-side periodic evaluation checks oldest open request age,
  dead-letter count, unread backlog, and stale-node evidence.
- [x] Threshold trips push a pane notification to the configured UI-facing node
  without requiring a status query first.
- [x] Thresholds are configurable and default to disabled.
- [x] Escalation push is documented as a scoped exception: threshold-push on
  runtime facts, not general product-policy escalation.

## 3. Evidence

- 2026-07-14: Audited the issue worktree status, diff summary, latest commit,
  GitHub issue body, and this task artifact. The branch was uncommitted, and
  the local diff matched the #621 convention-meter plus escalation-push
  acceptance criteria.
- Added `internal/projection.ProjectConventionMeterState` and per-node
  `nodes[*].convention_meter` status fields.
- Added daemon escalation evaluation through `maybePushEscalation`, called from
  scan ticks and routed through the existing pane notification sender.
- Added configuration keys:
  - `escalation_check_interval_seconds`
  - `escalation_oldest_open_seconds`
  - `escalation_dead_letter_count`
  - `escalation_unread_backlog_count`
  - `escalation_stale_node_seconds`
- Updated schema version from 4 to 5 for the new machine-readable status field.
- Documented the convention meter and the scoped escalation-push exception.
- 2026-07-14: Re-ran focused projection, daemon, CLI/status, config, and status
  tests; touched-package tests; diff checks; README/skill deprecated-reference
  scan; `nix flake check`; and `nix build`.

## 4. Verification

```sh
go test ./internal/projection ./internal/cli ./internal/daemon \
  ./internal/config ./internal/status -count=1
go test ./internal/projection \
  -run 'TestProjectConventionMeterStateCountsPerNodeViolations' -count=1
go test ./internal/daemon \
  -run 'TestEvaluateEscalationTripsThresholds|TestMaybePushEscalationSendsPaneNotificationOncePerTripSet' \
  -count=1
go test ./internal/cli \
  -run 'TestSessionStatusExposesConventionMeterPerNode|TestSessionStatusAddsSchemaV4SeverityForInputRequests|TestRunHelp_ConfigShowsUnifiedModelAndPublicKnobs' \
  -count=1
go test ./internal/config ./internal/status -count=1
git diff --check
nix flake check
nix build
```

- README/skill deprecated-reference scan: PASS, no matches.

## 5. Remaining Blockers

None.
