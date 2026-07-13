# Issue 619 Audit Draw Activation

## 1. Task

Implement D2 audit draw activation for requester pass verdicts, including
durable draw events, sampled review-required mail, configurable audit target,
cross-session request linkage, and audit-caught failure weighting.

## 2. Original Checklist

- [x] Compute `p_review = max(p_min, 1 - LCB)` per identity/work-class with a
  configurable nonzero `p_min`.
- [x] Verify cold-start `p_review = 1`.
- [x] Trigger from recorded `verdict_event` data with `verdict: pass`.
- [x] Journal `audit_draw_event` with sampled outcome and verdict reference.
- [x] Send sampled claims as review-required mail to a configurable audit
      target.
- [x] Apply a 2-3x audit-caught failure multiplier to track records.
- [x] Document the cooperative shared-filesystem visibility boundary.
- [x] Run focused tests, `nix flake check`, and `nix build`.

## 3. Evidence Log

- 2026-07-13: Guardian rejected the initial bounded slice because it only
  journaled `audit_draw_event`, parsed mailbox frontmatter directly, lacked a
  task artifact, did not route sampled draws to a review-required target, did
  not verify cross-session request linkage, and did not weight audit-caught
  failures.
- 2026-07-13: Reworked the slice to record durable `verdict_event` payloads,
  build draws from replayed verdict data, enqueue sampled review-required audit
  mail, and carry the audit request identifiers back into `audit_draw_event`.
- 2026-07-13: Focused tests passed for projection, daemon, config, and CLI
  surfaces.
- 2026-07-13: `nix flake check` passed.
- 2026-07-13: `nix build` passed.
- 2026-07-14: Audited the issue worktree status, staged and unstaged diff
  summary, latest commit, GitHub issue body, and this task artifact. The
  branch was still uncommitted but the implementation matched the issue
  acceptance criteria.
- 2026-07-14: Re-ran focused projection and daemon regressions, touched-package
  tests, `git diff --check`, `nix flake check`, and `nix build`; all passed.

## 4. Decisions

- The audit trigger is a durable `verdict_event`. Mailbox projection payloads
  may still be the source that records verdict events, but audit draw execution
  consumes the recorded event payload, not the mailbox event directly.
- The audit target defaults to a valid configured `command_approver_node` and
  can be overridden with `audit_target`. If neither is configured, sampled
  draws are still journaled and the daemon logs that no audit target is
  available.
- The shared local journal visibility limitation is documented in code because
  invisibility is cooperative until identity attestation and ACL work exist.

## 5. Changed Files

- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/config/postman.default.toml`
- `internal/cli/helptext/config.txt`
- `internal/cli/help_test.go`
- `internal/daemon/daemon.go`
- `internal/daemon/audit_draw_test.go`
- `internal/projection/audit_draw.go`
- `internal/projection/audit_draw_test.go`
- `.task-artifacts/issue-619-audit-draw-activation.md`

## 6. Verification

- `go test -timeout 60s ./internal/projection ./internal/daemon
  ./internal/config ./internal/cli`
- `go test ./internal/projection -run
  'TestComputeAuditReviewProbability|TestBuildAuditDrawPayload' -count=1`
- `go test ./internal/daemon -run
  'TestRecordMailboxProjectionPayload|TestAuditTargetFromConfig' -count=1`
- `git diff --check`
- `nix flake check`
- `nix build`

## 7. Blockers

- None currently.

## 8. Completion Verdict

- PASS. The original checklist is complete, including the post-review rework
  blockers around task artifact durability, sampled audit routing, verdict-event
  triggering, cross-session linkage, and audit-caught failure weighting.
