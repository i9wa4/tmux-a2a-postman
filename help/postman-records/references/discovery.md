# Local Markdown Artifacts — discovery

## 1. Work-Start And Resume Discovery

Search across dates before creating a new artifact, using the existing root:

```sh
mkmd_root="${POSTMAN_LOGBOOK_ROOT:-${MKMD_BASE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/tmux-a2a-postman/%logbook}}"
find "$mkmd_root" -type f -name '*.md' -print 2>/dev/null |
  sort -r
```

Sorting is only a candidate listing. Inspect trusted supplied/active references
first, then matching repo/branch sessions, related branch/worktree records and
matching task/issue labels. The repo key is remote owner/repository, or
`local/<repo-dir>` without origin. Branch directories render `/` as `-`, so
verify the full branch from trusted task state and contents; different full
branches can have the same rendered name. Verify Git top-level/worktree
relationship and purpose from the title, task, checklist, evidence and creation
provenance. A related worktree is not automatically the same owning scope.

Reject nonmatching candidates. A completed ordinary task may be stale for a
new task; subordinate completion does not retire its persistent orchestration
index. For ordinary same-purpose candidates, a trusted current canonical
reference wins; otherwise adopt a clearer/newer candidate only after inspecting
contents and provenance, and record the rejected pointer. Protected-index
ambiguity follows section 1.2 instead: preserve candidates and reconcile.
Keep distinct-purpose files and name their roles in handoffs. Do not merge
content ad hoc, choose solely by path/mtime or create another tracker simply
because recovery is inconvenient.

### 1.1. Reviewed Selection Scenarios

These are authored contract examples, not behavioral proof of an agent.

| Context                                                         | Selection and required action                                                                                                     |
| --------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Verified active orchestration A; later supplied same-purpose B  | Keep A; classify B and reconcile before affected work.                                                                            |
| Human explicitly authorizes A to B                              | Delegate migration with coverage/evidence map; B becomes canonical only after verification and recorded handoff.                  |
| Known A missing or unreadable; B happens to exist               | Recovery/conflict, not absence; neither adopt B nor create another index.                                                         |
| Midnight passes while the scope is unchanged                    | Reuse A across date directories.                                                                                                  |
| A child task completes and a new subordinate task starts        | Reuse A and reconcile its task entries; no fresh branch tracker.                                                                  |
| Candidate is from another full branch or unrelated worktree     | Reject scope mismatch; verify that scope's own record instead of sharing A.                                                       |
| Full branches render to the same directory label                | Compare full identities and provenance; the rendered name cannot establish sameness.                                              |
| Two same-purpose candidates remain equally plausible            | Reconcile before adoption or completion; newest mtime is insufficient.                                                            |
| No active pointer or valid candidate exists                     | One authorized writer creates one artifact using this topic; manager verifies the returned path.                                  |
| Supplied ordinary research B while orchestration A is active    | B is a distinct-purpose artifact, not replacement of A.                                                                           |
| Requester supplies ordinary plan B while ordinary plan A exists | Preserve ordinary supplied-path intent and existing write authority; do not impose orchestration-only human replacement approval. |
