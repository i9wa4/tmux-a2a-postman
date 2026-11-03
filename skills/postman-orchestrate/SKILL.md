---
name: postman-orchestrate
license: MIT
description: |
  USE FOR: Coordinate delegated tasks and preserve original acceptance through handoff.
  DO NOT USE FOR: Read the passive mailbox status without coordinating a task.
---

# postman-orchestrate

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman skills postman-orchestrate <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                                          | ref                                          |
| -------------------------------------------------- | -------------------------------------------- |
| Accepting a task and assigning ownership           | [`intake`](references/intake.md)             |
| Coordinating work, updates and exact replies       | [`coordination`](references/coordination.md) |
| Checking acceptance and review                     | [`completion`](references/completion.md)     |
| Resuming or recovering failed delivery             | [`recovery`](references/recovery.md)         |
| Delegating across parent/child teams or publishing | [`hierarchy`](references/hierarchy.md)       |
