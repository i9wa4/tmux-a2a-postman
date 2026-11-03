# Local Markdown Artifacts — retention

## 1. Scope And Retention

- Branch or worktree-specific artifacts should stay under the script-generated
  branch session directory.
- Repo-independent scratch belongs in `tmp`; retain it until the user chooses
  deletion, including after it stops carrying handoff value.
- Do not create repo-local `plans/`, `research/`, or similar directories unless
  the requester supplied that repo path or the repository explicitly owns those
  artifacts.
- Local absolute paths are fine in private task artifacts and Postman traffic.
  Public GitHub surfaces should use repo-relative paths or stable URLs.

## 2. Recursive Team Scope Composition

Each child team's orchestrator uses the same canonical-path rules for its own
verified repo/full-branch/worktree scope. Keep the original delegated goal,
constraints, acceptance criteria, owner and exact parent request/return
references in that index. Its children can delegate again; depth does not
select another artifact convention or a special role prompt.

The parent index links child scope evidence and preserves its own original
acceptance criteria. Child detail files retain their parent reference, current
scope, owner, evidence, result and next action. Different workspaces/branches
do not share one mutable global index. Parallel tasks in one verified scope
can share its canonical index while keeping separate task/request IDs and
artifact owners. A parent is not complete merely because a child ACKed or a
mail reply obligation closed.

On restart, reuse and reconcile the same verified index and retained own mail.
Never replace it with a newly convenient file, manufacture missing progress,
apply an old result to a new request, or bypass canonical-path reconciliation.
The live Postman role contract owns delegation/review behavior; this reference
owns artifact location and composition, not a second scheduler.

## 3. Retention and Request Continuity

All records and logs remain until the user chooses to delete them. Labels such
as tmp describe purpose, not an automatic expiry policy. Do not delete evidence
merely because a task completed. Preserve the original human request and each
follow-up with an accepted, deferred-with-reason or evidenced-done disposition.
A notification interruption cannot replace the task. On every meaningful
handoff/resume reconcile these dispositions, owners, pending exact replies,
review lineage and next action. A file or transport ACK does not prove success.
