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
- 2026-07-13: Guardian rejected the second completion because `mergeConfig`
  omitted `verdict_grace_seconds` and `verdict_debt_cap`, malformed
  daemon-submit filenames could still be persisted when a valid sender was
  present, and lazy `verdict:none` timeout materialization was not idempotent
  under concurrent same-requester sends.
- 2026-07-13: Added explicit TOML presence tracking for the verdict config
  fields so merge overlays preserve non-zero values and intentional
  `verdict_debt_cap = 0`.
- 2026-07-13: Added daemon-submit filename parsing before content, verdict gate,
  or `post/` persistence.
- 2026-07-13: Added mutex-guarded replay dedupe around durable
  `verdict:none` timeout append, with a concurrent same-requester regression
  test proving only one timeout fact is recorded.
- 2026-07-13: Focused config and daemon tests passed after guardian rework.
- 2026-07-13: `git diff --check` passed after guardian rework.
- 2026-07-13: `go test -timeout 60s ./internal/daemon ./internal/projection
  ./internal/cli ./internal/config` passed after guardian rework.
- 2026-07-13: `nix flake check` passed after guardian rework.
- 2026-07-13: `nix build` passed after guardian rework.
- 2026-07-14: Guardian plus critic rejected commit `a676c6e` because
  piggyback verdicts were not applied to the same gated send, direct
  `post/` sends bypassed the daemon-only gate, explicit
  `verdict_grace_seconds = 0` had inconsistent runtime/projection semantics,
  timeout dedupe was not scoped to the current journal generation, the file
  inventory was incomplete, and non-default `ui_node` exemption lacked direct
  daemon-gate coverage.
- 2026-07-14: Moved verdict gate enforcement into `internal/verdictgate` and
  reused it from daemon-submit and direct CLI send paths.
- 2026-07-14: Applied outgoing `verdict`/`verdictOf` metadata to the current
  requester debt before rejecting a reply-required send, so a same-message
  piggyback verdict can satisfy the gate.
- 2026-07-14: Added direct-send enforcement before draft/post writes, using the
  current journal lease for durable timeout materialization.
- 2026-07-14: Preserved explicit `verdict_grace_seconds = 0` from loaded config
  and made projection treat `0` as immediate expiry; only negative grace values
  fall back to the default stale window.
- 2026-07-14: Scoped timeout dedupe to the current session key and generation
  so a prior-generation `verdict:none` fact cannot suppress the current
  generation's timeout append.
- 2026-07-14: Focused daemon tests passed:
  `go test ./internal/daemon -run 'Test(ProcessDaemonSubmitRequest_(SendRefusesReplyRequiredWhenVerdictGraceExpired|SendRefusesReplyRequiredWhenVerdictDebtExceedsCap|AllowsReplyRequiredWithPiggybackVerdict|SendExemptsMessengerFromVerdictGate|SendExemptsConfiguredUINodeFromVerdictGate|VerdictGateRejectsEnvelopeSenderSpoof|VerdictGateFailsClosedWithoutAuthoritativeSender|VerdictGateNormalizesSameSessionSender|RecordsVerdictNoneTimeout|ReturnsErrorWhenVerdictNoneTimeoutAppendFails)|EnforceVerdictGate_(DedupesConcurrentSameRequesterTimeout|TimeoutDedupeIgnoresPriorGeneration)|ConfigureVerdictGateFromConfig_(AllowsZeroVerdictDebtCap|ExplicitZeroGraceExpiresImmediately))'`.
- 2026-07-14: Focused CLI tests passed:
  `go test ./internal/cli -run 'TestRunSendHeredoc_DirectPathEnforcesVerdictGate|TestRunSendMessage_UsesDaemonSubmitForOwnedSessionInLegacyMode'`.
- 2026-07-14: `git diff --check` passed.
- 2026-07-14: `go test -timeout 60s ./internal/daemon ./internal/projection
  ./internal/cli ./internal/config` passed.
- 2026-07-14: `nix flake check` passed.
- 2026-07-14: `nix build` passed after staging the new
  `internal/verdictgate/gate.go` package so Nix included it in the source.

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
- Timeout idempotence: timeout materialization replays existing timeout facts
  and appends missing facts under one process-local mutex, which prevents
  duplicate durable timeout records for concurrent sends from the same
  requester in one process.
- Gate placement: daemon-submit and direct `send-heredoc` writes share the same
  verdict gate package; direct sends gate after message rendering and before
  draft/post persistence.
- Piggyback verdicts: an outgoing reply-required message's own verdict metadata
  is applied to the current projected debt before enforcement.
- Zero grace: explicit `verdict_grace_seconds = 0` means immediate expiry.
  Negative internal grace values retain the default fallback for projection
  callers that do not provide a configured value.
- Timeout dedupe scope: existing timeout facts only dedupe when they belong to
  the current session key and generation.

## 5. Changed Files

- `internal/cli/send_message.go`
- `internal/cli/send_message_test.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/daemon/daemon.go`
- `internal/daemon/daemon_submit_test.go`
- `internal/projection/daemon_submit.go`
- `internal/projection/mailbox_state.go`
- `internal/projection/message_reply_slot_state.go`
- `internal/projection/verdict_debt_state.go`
- `internal/verdictgate/gate.go`
- `.task-artifacts/issue-617-verdict-enforcement-gate.md`

## 6. Verification

- `git diff --check`
- `go test ./internal/config ./internal/daemon`
- `go test -timeout 60s ./internal/daemon ./internal/projection
  ./internal/cli ./internal/config`
- `go test ./internal/daemon -run 'Test(ProcessDaemonSubmitRequest_(SendRefusesReplyRequiredWhenVerdictGraceExpired|SendRefusesReplyRequiredWhenVerdictDebtExceedsCap|AllowsReplyRequiredWithPiggybackVerdict|SendExemptsMessengerFromVerdictGate|SendExemptsConfiguredUINodeFromVerdictGate|VerdictGateRejectsEnvelopeSenderSpoof|VerdictGateFailsClosedWithoutAuthoritativeSender|VerdictGateNormalizesSameSessionSender|RecordsVerdictNoneTimeout|ReturnsErrorWhenVerdictNoneTimeoutAppendFails)|EnforceVerdictGate_(DedupesConcurrentSameRequesterTimeout|TimeoutDedupeIgnoresPriorGeneration)|ConfigureVerdictGateFromConfig_(AllowsZeroVerdictDebtCap|ExplicitZeroGraceExpiresImmediately))'`
- `go test ./internal/cli -run 'TestRunSendHeredoc_DirectPathEnforcesVerdictGate|TestRunSendMessage_UsesDaemonSubmitForOwnedSessionInLegacyMode'`
- `nix flake check`
- `nix build`

## 7. Blockers

- None currently.

## 8. Completion Verdict

- PASS. The original checklist and follow-up review blockers are addressed in
  the issue worktree: source identity is authoritative and fail-closed, debt
  lookup is normalized, verdict config overrides are merged, malformed
  daemon-submit filenames fail closed before persistence, timeout append
  failures propagate, lazy timeout materialization is documented and
  idempotent under concurrent same-requester sends, piggyback verdicts satisfy
  the same gated send, direct `post/` sends use the gate, explicit zero grace
  expires immediately, timeout dedupe is generation-scoped, the configured
  `ui_node` exemption has daemon coverage, and the artifact file inventory is
  complete.
