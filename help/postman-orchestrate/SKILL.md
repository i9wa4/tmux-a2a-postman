---
name: postman-orchestrate
license: MIT
description: |
  USE FOR: Coordinate delegated tasks, parent/child directory teams, preset
  launch handoffs and original acceptance through each return hop.
  DO NOT USE FOR: Read the passive mailbox status without coordinating a task.
metadata:
  postman-skill-body: >-
    Read and follow `tmux-a2a-postman help postman-orchestrate`.
---

# postman-orchestrate

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman help postman-orchestrate <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                                         | ref                                          |
| ------------------------------------------------- | -------------------------------------------- |
| Accepting a task and assigning ownership          | [`intake`](references/intake.md)             |
| Coordinating work, updates and exact replies      | [`coordination`](references/coordination.md) |
| Checking acceptance and review                    | [`completion`](references/completion.md)     |
| Resuming or recovering failed delivery            | [`recovery`](references/recovery.md)         |
| Launching or delegating across parent/child teams | [`hierarchy`](references/hierarchy.md)       |
