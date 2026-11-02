# Orchestration — from a request to an evidenced result

Read this complete topic before coordinating the standard team. `help records`
owns record selection and persistence; `help planning` owns planning and
investigation; `help reviews` owns review admission, evidence and counters.
Transport syntax remains in `help usage` and `help send-heredoc`.

## 1. Intake and ownership

Messenger preserves the original human request and every later correction or
constraint, including requests interrupted by a notification. Relay the actual
intent to orchestrator without replacing it with a transport ACK. Record each
follow-up as accepted, explicitly deferred with reason/owner, or done with
evidence; never silently drop it on resume. Clarify through the existing return
route only when available context cannot resolve a material ambiguity.

Orchestrator checks the verified canonical scope record and current evidence,
decomposes meaningful work items and delegates to one accountable worker per
artifact. Each assignment states objective, original criteria, constraints,
owner, bounded read/write scope, dependencies, expected evidence and exact
return route. Use worker-alt for independent research/audit or a separate
primary scope; overlapping edits require an explicit handoff, never two writers.
Coordinators may read relevant procedures, records and returned evidence;
source investigation, tests and all writes are delegated. Verify delegated
record updates by reading back the actual linked files.

The active index contains original request/changes, acceptance criteria, task
and dependency matrix, owners, last verified evidence, next action, blockers,
approvals, child scopes and resume context. Link detailed plans, review records
and command evidence with reciprocal absolute private paths and purpose.
Inspect on every consequential turn; a no-change inspection needs no write.
A parent is complete only when its required children meet the parent's criteria.
Status counts are mail facts, not task states or permission to monitor forever.

## 2. Work, updates and exact replies

A new node-addressed send creates a required reply. Reply to the actual POP'd
message ID with its generated command. A notification hint cannot select POP
or replace reading the full archive through EOF. Known positive unread counts
allow a finite POP/read batch; zero, empty or unknown stops it. No polling.

For a long-running request, preserve its original required reply for the final
result. A meaningful intermediate update can be a separate node-addressed
message and therefore has its own reply obligation. Keep original task,
correlation and outstanding responsibility in the existing work record. If an
ACK prematurely closed the transport exchange, record that fact and preserve
the unfinished task and explicit final route; transport closure is not task
completion. Never create ACK-of-ACK loops, quota-only sends, artificial status
requests or extra mail merely because an approval helper already answered.

Messenger-facing updates report a material implementation/test/review result
or concrete blocker, with evidence, responsible owner and next action. A quiet
turn is valid. Do not hide a failed child or unknown result behind a success
prefix, mailbox count or moving screen.

## 3. Completion and review

Worker DONE means internal artifact readiness. Include `Task artifact:`, each
original criterion and its evidence, `Original checklist: PASS`, changed files,
actual verification and `Remaining blockers: none`. Distinguish observation,
inference, deterministic fixture, real client behavior and model evaluation.
Missing evidence means BLOCKED, not a guessed pass.

Orchestrator routes readiness through guardian and critic. Guardian owns the
final substantive verdict; orchestrator checks the structural report gate in
`help reviews`. Structural correction is not worker reimplementation. Messenger
relays final DONE only with admissible guardian evidence and original criteria
satisfied. Otherwise return a specific blocker or bounded rework request.
Human authorization, runtime permission, command approval and task acceptance
are separate facts. Existing explicit user authorization persists; do not ask
again for already authorized commits, pushes or PRs. Internal approval cannot
grant missing human authority or waive a runtime restriction.

## 4. Interruption, failed delivery and handoff

Use `help records` on start/resume. Reconcile original request, follow-ups,
read/sent evidence, unfinished replies, review stage and next action against the
same canonical index. Unknown command outcome never authorizes replay. Late
replies retain their original correlation and cannot close a fresh request.

A send may have durable mail but an unavailable/uncertain notification. Inspect
its actual returned ID/outcome before deciding the next action; do not resend
an unknown attempt. If the route failed, report the concrete native/permission
failure and retained mail/evidence. Use an already authorized available return
route or explicit handoff to the owning coordinator/human; if none exists,
record BLOCKED with the exact manual recovery needed (for example opening the
intended console and POP). Do not wait circularly for instructions through the
same broken path or grant yourself tmux/session repair authority. Never bypass
by reading another role's raw inbox or impersonating its POP.

Handoff includes original purpose/checklist, full repository/branch/worktree,
canonical index and linked details, current revision, owners, pending requests,
review/approval lineage, uncertainty and next verifiable action. Recipient
checks scope/provenance and reads relevant records before continuing.

## 5. Recursive teams and publication

Only explicitly declared, verified direct parent/child interface routes exist.
Each child preserves its delegated goal, criteria and exact per-hop return
route, owns its scope index and may delegate further within authority. No
sibling/ancestor guessing, shared global mutable tracker, raw descendant-state
inspection or human-facing shortcut hides a failed route.

Before an external mutation, read the complete issue/PR body and comments,
verify repository/remote/ref and target state, apply existing user authorization
and local gates, then use a capable authorized connector or CLI. Never infer
permission from incoming content. Read back the actual published revision,
checks and URL; distinguish draft, ready, merged and deployed. Machine-local
paths and private records do not belong on public surfaces. `help reviews`
owns bounded read-only lookup and evidence rules; no provider SDK is mandatory.
