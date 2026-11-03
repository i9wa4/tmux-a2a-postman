# Local Markdown Artifacts — selection

Use local markdown artifacts when work must survive chat compaction, handoff,
review loops, or terminal DONE/BLOCKED evidence. They are operational records,
not source changes or test substitutes.

Working records are AI/agent-facing memory kept locally, separate from
a client's rendered canvas. Human-facing Markdown can also
be prepared locally; use the readability guidance below. A readable document
does not require external publication.

This reference is the single deterministic location convention for where
working artifacts live in this environment: the path template in
[Location and Existing Records](creation.md) plus the
directory labels in
[Directory Labels](creation.md) together form that index. Other
skills should not restate this convention, the `mkmd` invocation contract, or
the label table — they should point here generically (for example, "leave a
durable artifact of your work") and let this file stay the one place that
changes when the convention changes.

## 1. Canonical Path Selection

This section is the sole selection/replacement procedure. Read it before any
supplied, discovered or newly created path is adopted. Identify purpose and
owning scope first: repository identity, full unsanitized branch, relevant
worktree relationship and task. Directory labels are discovery hints, not
proof of that identity. Do not add another registry or storage convention.

### 1.1. Ordinary Artifacts

For plans, research and other non-orchestration purposes, preserve this order:

1. The path supplied by the requester or trusted task/handoff.
2. The already-active artifact for that task and purpose.
3. A discovered artifact verified to match repository, branch, task and purpose.
4. A new artifact using this topic's creation procedure when none matches.

A requester may intentionally name a new ordinary plan path; it does not need
the protected orchestration replacement approval below. Preserve that intent
within existing write authority, and do not manufacture evidence for missing
contents. A genuinely different purpose may have its own file. Never split one
original checklist into competing trackers or combine distinct-purpose records
merely because their names look similar.

### 1.2. Protected Orchestration Index

For an orchestration index, identify the verified active canonical path before
applying ordinary supplied-path precedence. An active index remains canonical
for its verified repository/full-branch/worktree scope across dates and
subordinate-task completion. A later supplied path is not a replacement.

- If active A is readable, reuse it. For supplied B, determine whether B is a
  different-purpose reference, a mistaken pointer or an explicitly authorized
  replacement. Keep A while resolving the conflict; do not silently migrate.
- If A is known but missing/unreadable or its identity conflicts, preserve the
  known pointer and report recovery/conflict. This is not proof of absence and
  does not authorize creating or adopting B to continue affected work.
- Replacement requires current explicit human authorization naming A and B.
  Delegate the authorized migration to the existing writer: preserve contents,
  checklist/evidence and references with an old-to-new coverage map, verify the
  resulting content and reciprocal links, then record B in task state and
  handoffs. A proposal or authorization alone is not evidence of migration.
- If no active canonical path is known, use discovery below. Adopt a candidate
  only after checking repository, full branch, purpose, relevant worktree,
  contents and creation provenance. Ambiguous same-purpose candidates require
  reconciliation before affected work or completion claims; mtime, filename
  ordering or a newly supplied pointer alone cannot choose a winner.
- If no valid candidate exists and no known-path recovery is outstanding,
  create one index once using this topic's creation procedure under existing
  update ownership. A new branch's first index is not a replacement.

Managers own the decision; existing delegated one-writer rules govern all
creation, migration and updates. Verify the returned path/content and relevant
links before reporting records current. Selection supplies artifact mechanics,
not authority to change objectives, delegate tasks or implement source work.
