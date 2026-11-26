# Work records and shared decisions

## 1. Select one current record

Use the path supplied by a trusted handoff or the already active verified record
first. If none is known, inspect candidate Markdown across dates under
`${POSTMAN_LOGBOOK_ROOT:-${MKMD_BASE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/tmux-a2a-postman/%logbook}}`.
Verify repository, full branch, canonical workspace, owning layer, purpose and
contents; directory labels and modification times alone do not establish
identity. A known but missing or unreadable record is a recovery problem, not
permission to create another tracker. Reconcile ambiguous same-purpose
candidates before adopting one. Ordinary research or review notes may have
separate files.

Reuse the existing Logbook root and its environment precedence, plus the
verified workspace identity and `workspace.Name`; do not invent a new encoding,
date-based owner or registry. The default current management record is
`workspaces/<existing-workspace-key>/tasks.md`. Each verified workspace
coordinator writes its own record. A designated top-level orchestrator alone
writes `approvals.md` at that shared root; all local approvers may read it.
Parent scopes summarize reported child outcomes and decisions at their layer,
without mirroring child checklists or inspecting arbitrary descendant records.
Legacy records remain authoritative until a verified, authorized carry-forward;
upgrade does not erase or automatically migrate them.

## 2. Keep a complete scoped overview

Reading `tasks.md` fully should restore the original requests and accepted
changes, objectives and criteria, key decisions with reasons, current and
unresolved work, owners, blockers and next actions. Annotate links to detail
with the subject, relevant conclusion, when to consult it, and material revision
or evidence limit. Preserve the original creation time and zone of each item,
plus meaningful progress time when useful. An administrative edit, reassignment
or refresh is not task progress; if an old creation time is unknown, keep it
unknown. Preserve request/reply correlation, delegation and sources. An ACK or
partial update is not completion.

The owner reconciles open work on each turn and initiates the next authorized
action. Use fresh context when sufficient; read the record when needed and
persist meaningful changes. Do not require a full reread or rewrite on every
message. An older stalled item calls for reassessment of value, progress,
blockers and feasibility, not automatic expiry. Agent-chosen steps may change
within the agreed objective. If the requested outcome or criteria must change,
present a concrete continuation, alternative, deferral, scope change or
cancellation through the configured route. Keep the decision, pending owner and
revisit condition; do not repeatedly ask an unchanged question.

For the explicit request “このセッションの管理記録を、今の状態で作り直して”,
rebuild the addressed owner's record with the full current overview, decisions,
original item times, unfinished local and child outcomes, assignments,
correlation, evidence and next actions. Summarize finished history through
usable references. Verify all pending work and reasons carried forward, archive
the old version with a reference, and retain one current stable `tasks.md`.
Refreshing does not reset authority, criteria or identity. If managed children
are explicitly included, route to each child coordinator to refresh its own
record; the parent does not write child records.

## 3. Record human decisions faithfully

`approvals.md` lists current reusable human decisions and scoped conditions. For
each, retain target/action, allow or deny judgment, project/work/time limits and
exceptions, confirmation time, and the source containing the actual question and
answer. Keep revocations distinct from prior approvals. The designated top-level
writer records reusable answers and returns question, answer, source and scope
through verified parent and child routes. An empty memo adds no permission gate:
valid human authorization supplied in a current request remains usable. A typed
command decision, reviewer inference, route or successful execution creates no
general human permission. Match the actual command and context, asking only for
materially missing scope. User-confirmation waits belong in `tasks.md`;
individual executions remain in existing archives. Independent roots need
separate writer assignment, not accidental concurrent edits of one Markdown
file.
