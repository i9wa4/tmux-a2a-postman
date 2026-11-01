# Issue 937 completion audit

This maps the current, maintainer-approved scope of
[issue #937](https://github.com/i9wa4/tmux-a2a-postman/issues/937) to the
implementation and executable evidence in
[PR #958](https://github.com/i9wa4/tmux-a2a-postman/pull/958).
The latest six-command agreement supersedes historical four-command and
execute-bash wording. Retention is user-managed; all saved logs and observations
remain until user deletion. This audit does not treat the closed historical
PRs as completion evidence.

## 1. Acceptance evidence

Test names below are in the linked source files. The entire suite runs through
`go test ./...`; Nix builds run the same process tests. Final platform/check
results belong to the PR's current commit, rather than an older passing run.

| Issue criterion                                    | Implementation and evidence                                                                                                                                                                                                                                                                                                            |
| -------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1. Daemonless operations and binary replacement    | [Rebuilt standalone CLI](../../internal/app/workspace_no_daemon_cli_process_test.go), [end-to-end process tests](../../internal/app/workspace_no_daemon_e2e_process_test.go), and all six commands in [dispatch](../../internal/cli/dispatch.go). No startup service or delivery worker.                                               |
| 2. Stable HOME-relative storage                    | [Root/name tests](../../internal/workspace/workspace_test.go) cover escaping, collision and outside-HOME rejection. [Ownership tests](../../internal/workspace/ownership_test.go) cover concurrent registration, persistence and takeover. The rebuilt CLI tests cover cd and replacement.                                             |
| 3. Ownership before effects                        | Ownership tests reject live/unknown owners, unsafe locks and changed identity. [Native CLI tests](../../internal/app/workspace_cli_native_test.go) exercise retitle/respawn races before local and cross-session effects.                                                                                                              |
| 4. Direct parent/child interfaces                  | [Native cross-session mail tests](../../internal/workspace/tmux_test.go), native CLI status tests and [terminal-driven process matrix](../../internal/app/workspace_notification_process_test.go) cover both directions, return correlation and interface visibility.                                                                  |
| 5. Foreground delivery and input notification      | [Publication/recovery](../../internal/workspace/mail_test.go), [notice tests](../../internal/workspace/notification_test.go), [observed-input regressions](../../internal/workspace/notice_observer_test.go) and the actual-input terminal matrix. Disabled/absorbed input cannot drive a rally.                                       |
| 6. Receiver-owned POP and history                  | Mail tests cover each claim interruption boundary. [Concurrent process claims](../../internal/app/workspace_pop_concurrent_process_test.go), [durable first claim](../../internal/app/workspace_pop_claim_process_test.go) and [broken stdout](../../internal/app/workspace_pop_output_failure_process_test.go) prove no resurrection. |
| 7. Compact post-operation self                     | [Public protocol tests](../../internal/app/workspace_protocol_process_test.go), [saved status/capture failure](../../internal/app/workspace_status_process_test.go) and [summary contract](../../internal/workspace/summary_contract_test.go). Empty POP has a null path, stable POP time and capture opportunity.                     |
| 8. Exact obligations and unknowns                  | Summary contract and [exact state tests](../../internal/workspace/state_test.go) cover reply arrival before POP, combined replies, unrelated correlation and unknown facts. Task acceptance is not inferred from these counts.                                                                                                         |
| 9. Read-only get-status                            | Status process tests compare every stored file before/after. Native CLI status tests restrict related rows to direct interfaces. Saved timestamps do not refresh themselves or imply live activity.                                                                                                                                    |
| 10. Session snapshots                              | [Real-tmux session tests](../../internal/workspace/session_capture_test.go) cover all windows, unconfigured panes, shared-window exclusion, text/Unicode limits, 66 panes, concurrent capture, slow capture and retained history. Measurements below include full process/EOF costs.                                                   |
| 11. Reviewed execution conditions                  | [Approval process matrix](../../internal/app/workspace_execute_approval_matrix_process_test.go) covers absence, reviewer loss, config/probe failure and late replies. [Executor tests](../../internal/workspace/execution_test.go) cover the fixed 300-second deadline, exact decisions and dispatch boundaries.                       |
| 12. Exact waiter and unlocked execution            | [Decision/waiter implementation](../../internal/workspace/execution_decision.go), [public approval tests](../../internal/app/workspace_execute_approval_process_test.go), executor tests and the eight-request burst. Other inbox mail stays unread; no lock spans approval waiting or command execution.                              |
| 13. Separate, current skills                       | [Send](../../skills/postman-send-message/SKILL.md), [usage](../../skills/postman-usage/SKILL.md), [config auditor](../../skills/postman-config-auditor/SKILL.md). Manual behavioral fixtures are reconciled; deterministic trigger gating remains #830 and recall remains #954.                                                        |
| 14. Development instructions                       | [AGENTS](../../AGENTS.md) is canonical; [CLAUDE](../../CLAUDE.md) points there. [Contributing](../../CONTRIBUTING.md) documents owned tmux fixtures, Nix checks and optional real-client validation.                                                                                                                                   |
| 15. Docs/help/examples/release surfaces            | [CLI specification](../cli-spec.md), README, command help and skills share the six-command interface. Historical Herdr research is marked discontinued. No manual-bundling or new release owner is introduced.                                                                                                                         |
| 16. Obsolete code removal, retained pure helper    | Unused capture registration/lease/pruning and old CLI policy branches are deleted. [Compaction marker parsing](../../internal/idle/idle.go) and its tests remain isolated, without a runtime observer or notification dependency.                                                                                                      |
| 17. Owned rebuilt-candidate fixtures               | [Fixture lifecycle tests](../../internal/testfixture/real_tmux_test.go) cover partial setup, command cancellation, scenario failure, concurrent runs and unrelated sentinels. HOME/XDG/startup/history are private. See interruption limits below.                                                                                     |
| 18. Full input/output/log inventory                | [CLI inventory and complete result examples](../cli-spec.md#10-complete-interface-and-logging-inventory), public protocol/stream tests and [unknown execution audit test](../../internal/app/workspace_execute_process_test.go). No stdout/stderr retention platform is added.                                                         |
| 19. Runtime-independent help and skill packaging   | Public protocol tests call every operational help entry without creating state; [removed-entrypoint tests](../../internal/cli/workspace_commands_test.go), metadata validation and skill-publish dry runs remain. Behavioral model evals are not silently made part of CI.                                                             |
| 20. Absorbed invariants and historical PR coverage | Items 2–6, 10 and 17 directly test naming/identity, retention, no-loss, cleanup and process boundaries. No dependency on merging #942/#944/#945, old projections, trust ledgers or a hierarchy/task engine.                                                                                                                            |

The audit found and corrected two omissions: uncertain execution JSON now names
its exact recovery audit, and capture persistence retries only pre-mutation
workspace-lock admission within its existing budget. A concurrent capture test
reproduced the latter failure before the fix. The audit also retained the pure
compaction helper required by criterion 16 and repaired stale documentation and
manual behavioral fixtures. macOS CI additionally exposed the old two-second
dispatch-audit admission timeout under eight approved concurrent requests.
Admission now waits up to ten seconds outside the workspace lock, within the
original caller/approval deadline; a longer-contention regression verifies
exactly one audited execution. Unknown ownership and failed audit writes still
prevent dispatch.
macOS also caught post-submit receipt writes inheriting the two-second input
deadline after the TUI had already received and POP'd the mail. The input budget
now starts after the pending guard and covers the actual editor observations;
ownership/receipt persistence uses the outer eight-second bound. A 2.1-second
post-submit persistence regression verifies acceptance without another paste or
Enter. The full matrix has a five-minute batch deadline; its individual waits
remain bounded.

## 2. Notification evidence and limits

The default fixture starts a real isolated tmux server and deterministic agent
editors. Only actual pasted text plus submitted Enter triggers receiver-owned
POP, full archive reading, exact replies and fixed approval choices. It covers
ten scenarios in plain and bracketed-paste modes, including eight requests from
four senders to one approver. No controller supplies recipient POP/reply
actions.

The separate real Codex 0.163.0 TUI lane uses a local fixed-response Responses
provider. Its eight cases exercise Codex's actual editor and tool execution;
model reasoning is replaced. It is optional and requires neither account nor
API key. Actual Claude TUI/model compatibility and arbitrary TUI behavior are
not established by those results.

Observed-input tests add slow rendering with unrelated background output,
unchanged displays that could mean either acceptance or a newline, persisted
pending-guard re-entry, recovery from fresh submission evidence, resize, native
replacement and capture failure. Ambiguous states send no repeated Enter or
paste. Human drafts and unrelated input writers are still outside the
Postman-only writer lock; the former #901 coordinator roadmap was withdrawn.

## 3. Snapshot cost and coverage

Measurements compare complete empty-POP subprocess invocations, including JSON,
stdout EOF, native identity checks, comparison, persistence and sync. The
control is built from the same source using a Go overlay that omits only the
single CaptureSession call. It is measurement-only, with no product flag or
disabled capture mode. Twelve paired warm runs per variant use private real-tmux
sessions with one pane per window on Linux x86_64. These are local samples, not
a cross-platform SLO or memory claim.

| Panes/windows | Control p50 | With capture p50 | Added p50 / p95    | Coverage per call      |
| ------------- | ----------- | ---------------- | ------------------ | ---------------------- |
| 2 / 2         | 46.07 ms    | 100.16 ms        | 53.34 / 60.59 ms   | 2 captured, 0 skipped  |
| 8 / 8         | 46.15 ms    | 114.8 ms         | 69.1 / 76.64 ms    | 8 captured, 0 skipped  |
| 24 / 24       | 47.94 ms    | 164.69 ms        | 115.73 / 133.02 ms | 24 captured, 0 skipped |
| 66 / 66       | 50.12 ms    | 310.99 ms        | 261.66 / 288.57 ms | 64 captured, 2 skipped |

Eight concurrent empty POPs on the eight-pane session all succeeded, with
379.11ms median / 531.14ms maximum process time. Eight observation manifests
recorded three complete captures and five explicit busy exclusions. This is
best-effort contention handling, not a claim that every concurrent call took
its own full snapshot. Delaying each native capture by 350ms produced no fresh
successful pane sample and returned in 1177.59ms, with all eight exclusions
recorded; previous successful clocks remained available. Raw measurement
summaries and the capture source digest are in
[the cost record](937-capture-cost.json).

The implementation uses synchronous, finite observation. Normal measured
sessions cover every eligible pane. The 64-pane cap, one-second capture budget,
per-frame 200ms/output bounds and 500ms save-admission budget make overload
explicit without spawning a worker or indefinite queue. Filesystem syscalls
cannot be preempted by a Go context; these budgets are not a hard real-time
guarantee on a stalled filesystem. The index retains earlier successful clocks
when a pane cannot be freshly saved, without deleting history.

## 4. What remains after merge

- First align the consumer through
  [dotfiles #489](https://github.com/i9wa4/dotfiles/issues/489) and
  [#485](https://github.com/i9wa4/dotfiles/issues/485): pin a landed upstream
  revision, select available current skills, and update role instructions and
  approval/status examples together. At this audit, dotfiles main still pins
  v0.9.5 and names execute-bash, inspect-input, old flow fields and the retired
  session-operator skill. A parser smoke alone cannot validate those agent
  instructions. Activation on the target machine and installed-fleet acceptance
  remain the human owner's step under #485.
- Use the existing-configuration smoke documented in CONTRIBUTING to verify the
  chosen postman.md in private panes. The fixed-response client checks do not
  replace a live user's acceptance of their own setup.
- #830: deterministic positive/negative skill-trigger gating in Nix. Metadata
  validation and publish dry runs do not satisfy it.
- #954: the recall skill, latest recorded day's own read/sent procedure and
  specific data/trigger fixtures. Core mail retention is ready; recall is not
  implemented here.
- #930: simplify Go/Nix toolchain selection. #740: migrate retained development
  artifacts before deleting their tracked directory.
- #767: the external filesystem inbox/outbox/read adapter and its activation.
  The latest ownership comment withdraws its old #901 prerequisite.
- dotfiles #489: recursive orchestration, task/progress source of truth and
  variable-depth role/config integration. Deterministic parent/child transport
  remains tested in Postman; no task/hierarchy engine is added here.
- #891 is research, not a core completion prerequisite.

The retired #765/#768/#803/#811/#901 roadmaps are not outstanding Postman-core
implementation gates. Current six-command behavior, ordinary mail delivery,
generic notification, reviewed execution and saved snapshots are owned here.

## 5. Interruption and verification boundaries

Graceful fixture cancellation/failure cleans owned resources and preserves
unrelated sentinels. SIGKILL, host failure or a deliberately escaped descendant
cannot execute cleanup handlers; retained private evidence may need manual
cleanup. Production command review is cooperative coordination, not an OS
sandbox or protection against a malicious shell configuration.

Final required checks are full Go tests, focused races, Nix flake checks/build,
lint, vulnerability scanning, skill checks and Linux/macOS PR CI. See PR #958
for the exact current head and results. The measured capture control and fixed
model provider are validation tools only; production never uses them.
