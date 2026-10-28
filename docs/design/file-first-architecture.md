# File-first workspace architecture

Status: proposed target contract. The acceptance criteria below gate adoption;
the document does not declare implementation or migration complete.

Here, a **workspace identity** is a stable HOME-derived name and state
directory. A **runtime binding** is the saved claim that a live tmux
server/session/pane/process incarnation owns a role in that workspace. A
**pane observation** is a timestamped, optional capture of a verified related
binding; it is evidence, never ownership or mail state. A **committed message**
has recoverable inbox and sent records even when recipient notification fails.

## 1. Trigger and scope

`send-heredoc`, `pop`, `get-status`, and `execute-bash` must work from saved
workspace files without a running Postman daemon. A command resolves the
caller's current tmux identity, verifies the workspace and its live binding,
then reads or mutates only the state that caller owns, except for a verified,
route-authorized delivery to the addressed recipient inbox. A parent and child
may exchange interface mail across independent tmux sessions through explicit
bindings; a same-named role in the sender's session is not a substitute for the
addressed remote role.

The user-visible send completes when mail is durable. It makes one bounded,
best-effort notification attempt in the same invocation, without waiting for
the recipient to POP or reply. A failed or ambiguous notification is reported
separately and cannot roll back committed mail or authorize a blind resend.

## 2. Safety invariants

- Workspace names are stable, readable, HOME-relative identities; path
  collisions are escaped and paths outside HOME are rejected. Restart and
  concurrent first registration must preserve the same identity.
- Every mutation checks the caller's live session, pane and process binding
  against the saved owner. Another live session cannot mutate that workspace.
  A sender may write only the addressed remote inbox through an authorized
  delivery transaction after verifying both roots, the interface route and
  recipient binding; it does not own other remote workspace state.
  Proven stale bindings may be replaced without deleting history; unknown
  ownership fails closed. Reads used for status do not implicitly mutate.
- Send commits the receiver inbox and sender sent copy with recoverable
  transaction state. POP moves the first unread message to its final read path
  before reporting success, records one claim, and never claims it twice.
  Send and POP expose file-derived unread and reply obligations, including an
  empty POP. An arbitrary message does not satisfy an exact required reply.
- Pane capture is optional and bounded to related panes. Missing, stale or
  conflicting activity evidence is `unknown`; a screen cannot override saved
  mail obligations or justify an unverified mutation.
- A pane hint may target only the committed message's verified recipient
  binding. A cross-session address is never resolved as a local same-named
  role. Generic tmux paste plus Enter is disabled because recipient input may
  be pending. A supported recipient app must provide one indivisible
  verify-and-submit operation, or a lease that excludes **every** input writer
  from before the blank-composer check until submission and its observation.
  The app submits once and returns the committed message ID and unchanged
  recipient incarnation. Separate blank capture, paste and Enter operations
  are not this capability. The immediate UI capture may be pending or
  ambiguous; if it names a submitted ID, that ID must match. A bounded
  settled capture must show that same ID submitted and the composer empty.
  Timeout, busy input, stale binding, unrelated UI or missing capability
  cannot be reported as `sent` or retried blindly.
- `execute-bash` treats a positively verified absent approver as advisory;
  an unknown or changing approver is not absence. With a present approver it
  accepts only a correlated reply bound to the exact literal Command text and
  fresh request identity, waits at most 300 seconds, leaves unrelated mail
  untouched, and gives repeated or concurrent
  invocations independent approvals. No shared mutation lock is held while
  waiting or running Bash. A dispatch audit is durable before execution;
  contention retries only before lock acquisition under a finite budget.
  A possibly partial audit write is never retried, and no audit record replays
  a command.

## 3. Acceptance criteria

These 17 conditions gate implementation and cutover:

1. All four core commands work with no daemon, including after CLI replacement.
2. Stable names, path safety, restart reuse and concurrent registration pass.
3. Live ownership prevents cross-session mutation; stale and unknown cases pass.
4. Independent parent/child mail, replies, copies and peer guidance pass.
5. Synchronous delivery and bounded notification preserve exact committed mail.
6. POP is single-claim and survives move, receipt and output failure windows.
7. Every send and POP reports self-mailbox obligations and known unread mail.
8. Required replies are exact and separate from general inbox attention.
9. Saved-file status works without daemon or mutation and reports uncertainty.
10. Related-pane capture is bounded, incarnation-aware and retained correctly.
11. Execute-bash covers advisory, approval, rejection, timeout and bad evidence.
12. Approval wait is independent of manual requester POP and shared locks.
13. The Postman skills have current, distinct responsibilities and references.
14. `AGENTS.md` owns instructions; `CLAUDE.md` points to it.
15. The documentation and skill keep/update/delete/new matrix is complete.
16. Obsolete runtime/config/flags/tests are removed with measured
    simplification; compaction detection stays reusable without a mandatory
    observer.
17. Filesystem, concurrency and isolated real-tmux tests prove the no-daemon
    workflow without paid models or mutations of user state.

## 4. Durable state and routing

The user-selected root must resolve inside the canonical HOME. State lives
under `$XDG_STATE_HOME/tmux-a2a-postman` when set, otherwise
`$HOME/.local/state/tmux-a2a-postman`. A HOME-relative workspace name escapes
ambiguous path characters and cannot alias an outside-HOME root. Runtime tmux
identifiers may change on restart; they do not rename the workspace.

```text
state-root/
  registry/
    workspaces.json               routing/discovery index
  locks/
    <workspace-name>.lock
  <workspace-name>/
    workspace.json                 stable root/name identity
    runtime/binding.json           current owner and role bindings
    agents/<role>/
      draft/                       recoverable send composition
      inbox/                       committed unread mail
      read/                        final POP claims and receipts
        <message-id>.md
        <message-id>.pop.json
      sent/                        independent sender copies
      failed/                      recoverable failed delivery
      pane/                        bounded related-pane observations
        latest.txt
        latest.json
        history.jsonl
    journal/events.jsonl           necessary audit facts, not mailbox truth
    transactions/<operation-id>.json  interrupted-operation evidence
```

The registry is a reconstructible routing index, while each agent's mail files
are the mailbox source of truth. See
[Mail Facts and Node State](node-state-machine.md) for claim and reply
transitions, and [Notification Design](notification.md) for the post-commit
recipient cue and observation boundary.

The state root and workspace identity derive from HOME, not cwd. Changing
directory, restarting the CLI, or racing first registration must resolve the
same state path. Registration takes a workspace lock, checks the prelock and
postlock owner snapshots, validates live ownership, and writes a new binding
only for an unowned or *positively proven stale* incumbent. A live foreign
session is denied; missing or conflicting liveness evidence is unknown and
denied. A successful stale takeover retains all mail, transactions and
observations. The workspace mutation lock protects only the short state
transition; it is released before a notification, human wait, Bash execution
or pane capture. A separate role-transition lock remains held throughout a
related-pane capture so role assignments cannot change mid-capture. Cross-root
delivery acquires short locks for the verified sender and addressed recipient
roots in canonical order and publishes only the recipient inbox record and
sender copy under one recoverable transaction.

The `postman.md` frontmatter `workspace_tree` declares parent-child interface
routing for file-first adapters. It supplies allowed relationships, not live
ownership. Addressing an agent requires that relationship **and** a verified
recipient binding; a same-role pane in the sender's tmux session cannot stand
in for the remote child. Legacy TOML `workspace_tree`, split node files and
daemon PING configuration are not inputs to the four file-first commands.

Send records a transaction intent, then durably publishes the receiver inbox
record and sender sent copy. Recovery reconciles partial records without
inventing a second message. POP claims the selected ID or first eligible
inbox entry in filename order by moving it to the read path before returning
its body. A durable receipt makes a second claimant observe the existing
claim rather than re-emit it. Recovery
must handle interruption after move, after receipt, and during output. Each
send and POP derives unread counts and exact reply obligations from saved
mail; a missing reply is not satisfied by unrelated inbox traffic. Read-only
status does not register, take over, claim or repair a workspace.

## 5. Recipient UI and pane evidence

Role attribution starts with a trusted registration of role to binding at
workspace creation or an explicitly verified transition. A mutable pane
title, pane index, PID alone, or text displayed in a pane is not authority.
Before a related-pane capture, acquire the exclusive role-transition lock for
every independent selected root in canonical order, then verify socket/server,
session, pane, PID and incarnation against every registered selected target.
Hold all those locks through capture and final binding and epoch validation in
each root. Retitle must preserve the registered role only if the same
incarnation remains. Respawn or reuse of a pane ID invalidates the old
binding until re-registration. Do not scan unrelated fleet panes.

Every observation records role, pane, capture time, incarnation, source and
fingerprint. Bound both capture bytes/count and retention; age out historical
observations without deleting mail or reply obligations. If verification fails,
report `unknown`. Any optional reason-only diagnostic receipt must stay separate
from authoritative workspace mail and observation records. Negative tests must
cover retitle, respawn, stale time, unrelated panes and capture failure;
positive tests must prove a registered sender and receiver on their actual
session incarnations. Capture must not hold a mailbox lock, consume mail, modify
user input or interfere with send.

The mail commit precedes a notification attempt. Only a supported
app-owned transaction or enforceable all-writer lease may enter the recipient
composer. The transaction must bind the committed message ID, exact target
role and current server/session/pane/PID incarnation. It verifies an empty
composer while excluding competing writers, submits once, and obtains a
receipt or correlated observation while exclusion still holds. A bounded
settled observation may resolve an ambiguous immediate capture, but cannot
repair a mismatched nonempty immediate ID. Unknown outcome stays unknown:
never roll back mail, send a second copy, or press Enter again. A positive
real supported recipient UI transition is required before calling the
production adapter complete; a fake app proves only the contract.

## 6. Approval and execution

`execute-bash` uses saved workspace mail as an approval inbox. An advisory
run is allowed only after a positive absence determination for the configured
approver; stale or conflicting presence is unknown, not absence. With an
approver, the request contains the exact literal Command text and a fresh,
single-use request identity; a digest is audit metadata, not approval identity.
Only a decision from that approver bound to both exact values authorizes
one execution. Rejection, timeout, disappearance, wrong reviewer, late reply,
repeated invocation and concurrent invocations each have separate refusal or
fresh-request outcomes. A requester waits on its own approval record; it
does not manually POP the reviewer's inbox or consume unrelated mail.

Evidence commands and old approval records are data, not runnable
authorization. Any optional replay must first enforce a trusted artifact
root, reject path traversal and symlink escapes, and verify the content
hash only after containment. Automatic replay, if retained, is limited
to read-only or idempotent actions; a mutating action requires explicit
human approval at execution time. An incomplete evidence contract never
satisfies an evidence gate.

No workspace or mailbox lock is held across delivery, the bounded approval
wait, or Bash. Immediately before execution, record a durable dispatch audit
for the exact approval. Only contention *before* acquiring the audit lock may
be retried within a finite budget. Partial audit, changed identity, or
unknown approval state fails closed and never replays the command. Public
CLI process tests must distinguish advisory, present approver, timeout,
disappearance, probe error, wrong/late reply, repeated and concurrent cases.

## 7. Legacy cutover and rollback

An old session is not a new workspace merely because names or panes look
similar. Before conversion, require a trusted one-to-one mapping of old
session to new context, each old message ID to a new ID, each sender/recipient
role alias, and every reply-to edge. A short legacy hash, file order, pane
title and PID cannot establish these mappings. Each legacy archive body needs
verified original-recipient provenance; where that provenance is absent, a
trusted mapping and explicit operator authorization are required before that
body can be exposed to a new recipient. Missing, duplicate, cross-context,
cyclic or conflicting mappings refuse conversion.

Default to archive-only and leave legacy bytes unchanged. A stable manifest
of the old and new trees by relative path, type, mode, size and digest may be
taken only after the cutover fence proves all legacy and new writers quiescent.
A separately authorized conversion validates every mapping and path under
that fence, takes a disposable dry run and byte-identical backup, writes new
records in a temporary tree, validates rewritten reply links, then publishes
atomically. A durable cutover generation and phase record survives process
death: `prepared` and `published` durably deny startup and mutation to both
old and new writers, including after the fence owner's death. Every writer
must check the persisted generation and phase before startup and each
mutation; an absent fence process never implies permission to write.
`active` enables only the new family after verification. Recovery after
fence-owner death or partial publication reacquires and proves exclusion of
both families, reconciles the generation and phase against the stable source
and destination manifests, and either completes verification or restores the
snapshot only when both manifests still match and exclusion remains intact.
Phase or manifest drift preserves both trees, refuses destructive restore,
and leaves both families fenced for operator repair.
Missing or ambiguous phase, generation, fence ownership, or publication
evidence has the same fail-closed result; no crash path silently unfences a
writer. Old approval records become inert and cannot authorize new Bash;
there is never an interval with two executable approval writers. A failed
preflight starts no conversion. After a stable snapshot, rollback to its
manifest is allowed only when both source and destination manifests verify
unchanged and exclusion remains intact; otherwise preserve both trees and
keep both families fenced for operator repair. A second run must be
deterministic and idempotent. Real records need authoritative mappings before
any real cutover; synthetic fixtures establish mechanics only.

After cutover, file-backed workspace state is truth across context
compaction. Remove obsolete runtime/config/flags/tests only after their
replacement public process behavior, migration and rollback pass. Preserve
compaction detection as reusable observation without making an observer
mandatory. Compare old and new surface area, latency and memory under the
same fixture before claiming simplification.

## 8. Documentation and validation

[Issue #937 §8](https://github.com/i9wa4/tmux-a2a-postman/issues/937)
defines the exact keep, update, delete, and new-file dispositions. Obsolete
daemon session, PING, queue, event-loop, and inventory documents must not
remain as active operating guidance. The reusable compaction detection
boundary belongs in this architecture without a required observer.

A returned POP path is the existing receiver-owned final read path.
Human display may shorten a home path with `~`, but machine consumers
need an explicit absolute path and must read the complete archived
Markdown. Metadata never waives that read, and truncated stdout is
insufficient. Public project references use repository-relative paths
or stable web URLs.

The live user entry points are [README](../../README.md) and command help
(`help send-heredoc`, `help pop`, `help get-status`, and
`help execute-bash`). The approval details belong in
[command approvals](../command-approvals.md); the distinct agent-facing
procedures belong in [send-message](../../skills/postman-send-message/SKILL.md),
[postman-usage](../../skills/postman-usage/SKILL.md), and
[config-auditor](../../skills/postman-config-auditor/SKILL.md). A minimal
no-daemon command path is:

```bash
tmux-a2a-postman send-heredoc --to peer <<'POSTMAN_BODY'
Message body
POSTMAN_BODY
tmux-a2a-postman pop
tmux-a2a-postman get-status
tmux-a2a-postman execute-bash --command 'printf ok'
```

Validation uses isolated HOME/XDG roots and private tmux sockets. Public
process cases cover all four commands with the daemon absent, CLI rebuild or
replacement, HOME/cwd/restart/concurrent identity, owner-stale/unknown cases,
mail move/receipt/output crashes, exact replies, read-only status, the full
approval fault/concurrency matrix, and verified recipient UI evidence.
Cleanup compares pre/post files, panes, sockets and processes and must not
mutate user state or use a paid model. Stage new files before running
`nix flake check`; both `nix flake check` and `nix build` must pass after
implementation.

## 9. Adoption gates

Contract tests against a fake recipient app cannot substitute for a positive
UI transition in a supported real app. A generic tmux paste and Enter path
remains unsupported unless it can meet the all-writer transaction contract.
Related-pane capture needs trusted role registration before it can report
role-attributed activity. Real legacy records stay archived until their
authoritative session, message, reply and role mappings are available. A
synthetic dry-run validates mechanics only. Independent review and public
process evidence must confirm these gates before cutover.

## 10. Capture and cutover safety gates

The requirements in this section govern the role/capture and migration
procedures above. An implementation must enforce them before either procedure
can produce an accepted result.

### 10.1. Trusted role transitions during capture

The workspace owner is the sole authority for trusted role assignments. A role
transition must acquire the workspace's exclusive role-transition lock, advance
a durable, monotonically increasing role epoch, and publish the new binding
before releasing the lock. Pane titles, process names, and a caller-supplied
role are observations; none may advance the epoch or authorize capture.

A capture operation derives the candidate related roots from trusted bindings,
acquires each root's role-transition lock in canonical order, then selects and
verifies targets while holding all locks through capture and final validation.
It records each root's epoch and
the trusted binding for **every** selected target: workspace/session identity,
role, pane, server/socket, process incarnation, and registration generation.
Immediately before accepting the capture, it verifies that each root's epoch is
unchanged and that every selected target still has exactly the recorded
binding. A changed epoch, missing binding, changed incarnation, unregistered
pane, failed capture, or inability to acquire/hold any lock makes the entire
capture fail as unknown; no partial target output is treated as trusted.
Capture has a finite deadline and bounded output/retention, and releases every
lock on every exit path. The locks cover role transitions in all selected
roots and all capture targets; a pre/post identity comparison without that
exclusive interval is
insufficient.

For example, if a parent and two children are selected with recorded epochs for
all three roots and a child is reassigned before capture completes, that
reassignment must wait for its root's capture lock. If any epoch or binding
differs at validation, the whole result fails rather than returning the parent
and one child as a successful related-pane capture.

### 10.2. Quiescent legacy cutover

The migration fence is owned by the new workspace owner process. Before
snapshot or conversion, it must acquire an exclusive cutover fence that is
respected by **all** old and new writers. Its preflight must prove the old
daemon is stopped, old delivery and approval queues have no pending or
in-flight work, old writer locks are released, and every legacy writer is
either stopped or cooperates with the fence. It must also prove that no new
legacy writer can start while the fence is held. A new-only lock does not
establish this exclusion. Ambiguous liveness, an uninspectable queue or lock,
or a noncooperating old writer fails preflight before any conversion output
is published.

Only after that quiescence proof may the owner take a stable source manifest
and perform a disposable dry run. Applying converted records additionally
needs authoritative old-session-to-new-context, message/reply-ID, and
participant alias bindings plus the existing collision and rollback checks.
Immediately before atomic publication, while still holding the fence, the
owner must verify quiescence again, confirm it still owns the fence, and
compare the entire source manifest (paths, types, sizes, modes, and hashes)
with the snapshot. Any source or destination drift after preflight or dry run
aborts publication, preserves both trees, discards only proven
converter-owned temporary outputs, and keeps both writer families fenced for
operator repair. Rollback of partial publication is allowed only after both
manifests verify unchanged and exclusion remains intact. The fence remains
held through publication, post-apply verification, or a verified rollback;
release it only after one writer family is safely active and the other remains
excluded.

Crash/restart tests must kill the fence owner in both `prepared` and
`published`, then attempt old and new writer startup and mutation. All four
attempts must be denied until recovery verifies the persisted phase,
generation, and both manifests. Tests must inject source and destination
manifest drift in each phase and prove recovery neither restores over changed
bytes nor enables a writer; the original manifests and archive bytes remain
available for operator repair.

If quiescence preflight fails because an old writer is live or noncooperating,
no stable manifest is claimed. Preserve the original bytes without rewriting
them and report manifest and archive access unavailable until quiescence is
proved. Once a stable manifest exists, missing authoritative bindings keep
the records archive-only: expose historical unread and claimed messages only
through a read-only archive view with their original state and verified
recipient provenance. Each archive body read, including a read reached from
list, status, unread, or claimed views, additionally requires a trusted
current viewer-to-original-recipient/session binding or an explicit operator
grant for that body. A same-role caller in an unrelated workspace is denied.
Without verified provenance, the body remains inaccessible until trusted
mapping and explicit operator authorization;
never auto-deliver, auto-claim, or relabel them as new workspace mail. Their
disposition requires an explicit reviewed mapping and cutover decision.

For example, a stopped daemon with one queued approval is not quiescent, so
snapshot/conversion is refused. Likewise, a live old writer that ignores the
cutover fence forces archive-only handling even if all visible queues appear
empty. Neither case may silently start a dual-writer period.

If a record appears after the dry run, the publish-time manifest comparison
fails; both trees stay intact and fenced, and only proven converter-owned
temporary records are discarded. If quiescence holds but
an old unread or claimed message lacks a trusted ID mapping, the archive view
retains its original state read-only; it does not deliver or claim that mail.
