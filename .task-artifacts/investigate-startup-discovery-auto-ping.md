# Investigate Startup Discovery Auto-PING

## 1. Task

Investigate startup/discovery auto-PING behavior, determine whether missing or
duplicate sends are intended or regressions, and implement the smallest
appropriate fix.

## 2. Original Checklist

- [x] Reproduce or otherwise verify whether startup/discovery auto-PING is
  currently not being sent.
- [x] Determine whether the current behavior is intended or a regression.
- [x] If it is a bug, implement the smallest appropriate fix.
- [x] Use applicable skills before work.
- [x] Follow AGENTS.md project checks after implementation: stage new files if
  any, run `nix flake check`, run `nix build`, and check `README.md` plus
  `skills/*/SKILL.md` for deprecated references if behavior or commands
  changed.

## 3. Evidence Log

- 2026-07-09: Current-session discovery auto-PINGs were queued with
  `not_before_at=2026-07-09T23:04:15+09:00`, then resolved by operator TUI
  PING between `2026-07-09T23:04:04+09:00` and
  `2026-07-09T23:04:07+09:00`; no later current-session postman PING message
  files existed around the due time.
- 2026-07-10: Guardian rework proved the `pivot-terraform` 21:52 sequence was
  real duplicate mail, not a journal-only artifact. Examples include
  `read/20260709-215236-s510e-r5eaa-from-postman-to-worker.md` plus
  `read/20260709-215241-s510e-r2b60-from-postman-to-worker.md`, and
  `worker-alt` inbox files at `21:52:36` and `21:52:42`.
- 2026-07-10: Representative journal rows showed the race:
  `worker-alt` seq 1582 `operator_tui` delivered at
  `2026-07-09T21:52:45.759251679+09:00`, then seq 1598 empty-resolution
  delivered at `2026-07-09T21:52:51.673744801+09:00` for the same
  `node_key`, `triggered_at`, and `not_before_at`; `messenger` seq 1591/1603
  and `guardian` seq 1592/1608 showed the same pattern.
- 2026-07-10: Root cause was stale auto-PING dispatch state: the auto path
  projected pending wake debt once, then could send after a direct operator
  PING had already resolved that debt.
- 2026-07-10: A late auto-path projection refresh fixed stale snapshots that
  were already recorded in the journal, but guardian identified a remaining
  check-then-act race where operator/manual and auto paths could both send
  before either recorded delivery.
- 2026-07-10: Final code fix added `internal/autoping/reservation.go`, a shared
  in-process reservation keyed by pending wake identity (`session_dir`,
  `node_key`, `pane_id`, `reason`, `triggered_at`, `not_before_at`). Auto,
  operator/manual, and compaction PING paths now contend on that reservation
  before sending matching pending wake mail.
- 2026-07-10: Documentation was updated in `docs/ping-events.md` to describe
  the expanded duplicate-control key, shared reservation, observable skip text
  `PING skipped for <node>: matching auto-PING already in flight`, and the
  two-daemon limitation.

## 4. Decisions

- The original current-session non-auto-send observation was intended behavior:
  operator TUI PING resolved pending auto wake debt before due time.
- The `pivot-terraform` 21:52 sequence was a real duplicate-delivery bug and
  violated the documented auto-PING duplicate-control contract.
- The final fix uses both a late projection check and a shared in-process
  reservation. The late check handles stale state already recorded in the
  journal; the reservation closes the in-process operator/manual-vs-auto
  check-then-act window before mail is written.
- Known limitation: the reservation serializes within one daemon process only.
  Abnormal two-daemon state is not covered by this change and would require a
  journal-backed or file-lock claim to serialize across processes. This is a
  non-goal for the current task because AGENTS.md already defines the
  two-daemon state as an operational repair scenario handled by the daemon
  restart procedure.
- Intentional operator retries remain allowed when no pending auto wake debt
  exists for the target node/pane.

## 5. Changed Files

- `internal/autoping/reservation.go`
- `internal/cli/start.go`
- `internal/cli/start_test.go`
- `internal/daemon/runtime.go`
- `internal/daemon/runtime_test.go`
- `docs/ping-events.md`
- `.task-artifacts/investigate-startup-discovery-auto-ping.md`

## 6. Verification

- PASS: Focused daemon, CLI, and projection tests for stale auto-PING
  suppression, shared wake reservation, direct PING resolution, startup
  queueing, delayed due delivery, and projection state.
- PASS: `go test ./internal/autoping -count=1`
- PASS: README/skill reference scan found no deprecated command, flag, or
  package references relevant to this behavior change.
- PASS: `nix flake check`
- PASS: `nix build`
- PASS: `git diff --check`

## 7. Blockers

- None. The two-daemon residual is a scoped non-goal documented above, not a
  blocker for this task.

## 8. Completion Verdict

- Original checklist: PASS
