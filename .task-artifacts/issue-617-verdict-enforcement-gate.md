# Issue 617 Verdict Enforcement Gate

## 1. Task

Implement the D1 verdict enforcement gate so requesters with unstamped accepted
fills must issue a verdict before sending more reply-required work.

## 2. Original Checklist

- [x] Enforce reply-required sends against requester verdict debt.
- [x] Respect `verdict_grace_seconds`.
- [x] Respect `verdict_debt_cap`, including `0`.
- [x] Exempt the configured human-facing UI node.
- [x] Bind daemon-submit requester identity to an authoritative submit source.
- [x] Fail closed when daemon-submit source identity is missing or inconsistent.
- [x] Normalize same-session qualified requester identities before debt lookup.
- [x] Materialize durable `verdict:none` timeout events with append errors
      propagated.
- [x] Define and test timeout materialization timing.
- [x] Run focused tests, `nix flake check`, and `nix build`.
- [x] Commit the finished worktree.

## 3. Evidence Log

- 2026-07-13: Guardian rejected the first completion because sender identity
  was still caller-controlled by filename, malformed filenames failed open,
  same-session qualified senders bypassed debt lookup, timeout append failures
  were only logged, timeout materialization timing was ambiguous, and there was
  no issue-specific task artifact.
- 2026-07-13: Added `DaemonSubmitRequest.Sender`, populated it from the CLI's
  resolved sender identity, and made the daemon fail closed for reply-required
  sends when that authoritative sender is absent or disagrees with the envelope.
- 2026-07-13: Reused projection identity normalization for same-session
  qualified senders before exemption and debt lookup.
- 2026-07-13: Made `verdict:none` timeout recording return append errors so the
  blocked send records an explicit failure instead of silently continuing.
- 2026-07-13: Focused daemon, projection, CLI, and config tests passed.
- 2026-07-13: `nix flake check` passed.
- 2026-07-13: `nix build` passed.
- 2026-07-13: Worktree prepared for a clean commit.

## 4. Decisions

- Authoritative sender source: daemon-submit requests carry a `sender` field
  written by the CLI after route and pane identity resolution. The daemon no
  longer derives verdict-gate requester identity from the message filename.
- Compatibility: the new `sender` field is optional in JSON for non-send and
  non-reply-required requests, but reply-required daemon sends fail closed if it
  is missing.
- Timeout materialization: expired verdict debt is materialized lazily when the
  requester next attempts a reply-required daemon send. Projection remains
  read-only; the gate writes durable `verdict:none` events before refusing that
  send.

## 5. Changed Files

- `internal/cli/send_message.go`
- `internal/cli/send_message_test.go`
- `internal/daemon/daemon.go`
- `internal/daemon/daemon_submit_test.go`
- `internal/projection/daemon_submit.go`
- `internal/projection/message_reply_slot_state.go`
- `internal/projection/verdict_debt_state.go`
- `.task-artifacts/issue-617-verdict-enforcement-gate.md`

## 6. Verification

- `git diff --check`
- `go test -timeout 60s ./internal/daemon ./internal/projection
  ./internal/cli ./internal/config`
- `nix flake check`
- `nix build`

## 7. Blockers

- None currently.

## 8. Completion Verdict

- PASS. The guardian rework blockers are addressed in a clean committed issue
  worktree: source identity is authoritative and fail-closed, debt lookup is
  normalized, timeout append failures propagate, lazy timeout materialization is
  documented and tested, and the issue-specific artifact exists.
