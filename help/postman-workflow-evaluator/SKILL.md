---
name: postman-workflow-evaluator
license: MIT
description: |
  USE FOR: An explicit human request to evaluate a named tmux session or task
  against its original objective and propose evidence-backed workflow improvements.
  DO NOT USE FOR: Passive monitoring, routine mailbox handling, or an unrequested
  audit of another session.
metadata:
  postman-skill-body: >-
    Read and follow `tmux-a2a-postman help postman-workflow-evaluator`.
---

# postman-workflow-evaluator

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman help postman-workflow-evaluator <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                                 | ref                                            |
| ----------------------------------------- | ---------------------------------------------- |
| Always for an explicit session evaluation | [`investigation`](references/investigation.md) |
| Always for an explicit session evaluation | [`evaluation`](references/evaluation.md)       |

This developer workflow begins with observation. A named session does not grant
access to another node's unclaimed mail, private files, credentials, or control
plane. Findings are proposals; edits, Issue/PR creation, replay, and publication
need their own authorization.
