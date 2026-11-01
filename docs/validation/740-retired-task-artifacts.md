# Retired task evidence (#740)

The tracked `.task-artifacts/` directory was retired after inspecting all nine
files at the cleanup revision. It had no current runtime producer, consumer or
required operating instruction. This index preserves the useful conclusions
and original evidence without presenting removed daemon features as current
behavior. New public evidence belongs with its owning issue/PR or an appropriate
document under `docs/`; private working notes can remain outside the repository.

## 1. Retention mapping

Every original below is an immutable link to the public, merged revision
`caaaa62f52d00ff6a8e85c2452e4b0af8706ae5b`. Original relative links remain
resolvable there. The original files, including recorded test commands, are
historical evidence rather than instructions to run against today's tree.

| Original file                                                                                                                                                                                          | Owner and retained information                                                                                                                                                     | Disposition                                                                                                                           |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| [952-smoke.md](https://github.com/i9wa4/tmux-a2a-postman/blob/caaaa62f52d00ff6a8e85c2452e4b0af8706ae5b/.task-artifacts/952-smoke.md)                                                                   | #952 / #953: former Bash receipt-handler smoke, exact claim/reply IDs and full-file digest. The handler has since been removed.                                                    | Retain original through this fixed link; remove tracked copy. PR #953's evidence remains available in its original commit/history.    |
| [952-tmux-notification-smoke.md](https://github.com/i9wa4/tmux-a2a-postman/blob/caaaa62f52d00ff6a8e85c2452e4b0af8706ae5b/.task-artifacts/952-tmux-notification-smoke.md)                               | #952 / #956: original generic tmux matrix and optional real Codex TUI with deterministic local responses; not evidence of paid-model comprehension or human-draft protection.      | Retain original. Current verification is in [CONTRIBUTING](../../CONTRIBUTING.md) and the [#937 acceptance audit](937-acceptance.md). |
| [investigate-startup-discovery-auto-ping.md](https://github.com/i9wa4/tmux-a2a-postman/blob/caaaa62f52d00ff6a8e85c2452e4b0af8706ae5b/.task-artifacts/investigate-startup-discovery-auto-ping.md)       | [8a6313f](https://github.com/i9wa4/tmux-a2a-postman/commit/8a6313f): operator/manual versus auto-PING race, shared in-process reservation, and the explicit two-daemon limitation. | Retain historical diagnosis; do not revive the removed daemon/PING implementation.                                                    |
| [issue-617-verdict-enforcement-gate.md](https://github.com/i9wa4/tmux-a2a-postman/blob/caaaa62f52d00ff6a8e85c2452e4b0af8706ae5b/.task-artifacts/issue-617-verdict-enforcement-gate.md)                 | #617: authoritative sender, recipient/filler matching, zero/negative configuration semantics and generation-scoped, cross-process timeout deduplication.                           | Retain historical decisions and review/test trail; removed verdict gate is not current policy.                                        |
| [issue-618-backfill-verdict-events.md](https://github.com/i9wa4/tmux-a2a-postman/blob/caaaa62f52d00ff6a8e85c2452e4b0af8706ae5b/.task-artifacts/issue-618-backfill-verdict-events.md)                   | #618: stable backfill event identity, explicit unknown historical evidence and nonmutating JSONL output.                                                                           | Retain historical contract; the old backfill command is not supported by the current CLI.                                             |
| [issue-619-audit-draw-activation.md](https://github.com/i9wa4/tmux-a2a-postman/blob/caaaa62f52d00ff6a8e85c2452e4b0af8706ae5b/.task-artifacts/issue-619-audit-draw-activation.md)                       | #619 / #647: durable verdict-triggered audit routing and cooperative shared-filesystem visibility limits.                                                                          | Retain historical implementation/review evidence; no automatic audit service is implied.                                              |
| [issue-620-evidence-replay-contract.md](https://github.com/i9wa4/tmux-a2a-postman/blob/caaaa62f52d00ff6a8e85c2452e4b0af8706ae5b/.task-artifacts/issue-620-evidence-replay-contract.md)                 | #620 / #648: replay containment, side-effect classes, opt-in activation and nonretroactive gating.                                                                                 | Retain original historical contract; no current automatic evidence execution is implied.                                              |
| [issue-621-convention-meter-escalation-push.md](https://github.com/i9wa4/tmux-a2a-postman/blob/caaaa62f52d00ff6a8e85c2452e4b0af8706ae5b/.task-artifacts/issue-621-convention-meter-escalation-push.md) | #621: historical convention-meter projection and disabled-by-default daemon escalation thresholds.                                                                                 | Retain historical evidence; these fields and daemon ticks are not the current status contract.                                        |
| [pr647-648-ci.md](https://github.com/i9wa4/tmux-a2a-postman/blob/caaaa62f52d00ff6a8e85c2452e4b0af8706ae5b/.task-artifacts/pr647-648-ci.md)                                                             | #647 / #648: failed historical runs were stale relative to the examined heads; refresh availability and review gates remained separate.                                            | Retain exact heads/run IDs and diagnosis; do not interpret it as a present CI result.                                                 |

## 2. Current references and prevention

The current [CLI specification](../cli-spec.md),
[acceptance audit](937-acceptance.md) and
[contribution checks](../../CONTRIBUTING.md) own current behavior. No live
document linked the retired directory at cleanup time; references within its
historical files remain preserved at the fixed revision above. Existing GitHub
discussion URLs are intentionally left intact; use these fixed links if a
branch-relative historical link no longer resolves.

The root ignore rule prevents accidental additions. The flake check rejects a
tracked reintroduction, including a forced addition or an old branch merge.
These checks do not delete or inspect private copies in other worktrees.

## 3. Recovery and scope

Use Git history or the immutable originals to inspect or restore selected
tracked files into an appropriate new location. Reverting the cleanup also
restores the old tracked tree and its prevention policy together. Local-only
content is not recoverable from Git unless it was committed separately.

No other worktree, ignored/untracked note, branch-specific artifact (including
the old issue-677 record), runtime mailbox or local state directory is part of
this cleanup.
