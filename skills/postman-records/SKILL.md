---
name: postman-records
license: MIT
description: |
  USE FOR: Resume a task from its verified canonical work record.
  Preserve durable checkpoints and handoffs across restarts.
  DO NOT USE FOR: Look up mail counts without selecting or changing a work record.
---

# postman-records

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman skills postman-records <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                                             | ref                                    |
| ----------------------------------------------------- | -------------------------------------- |
| Selecting or replacing a canonical record             | [`selection`](references/selection.md) |
| Starting or resuming work                             | [`discovery`](references/discovery.md) |
| Creating or locating an artifact                      | [`creation`](references/creation.md)   |
| Checking scope, continuity or retention               | [`retention`](references/retention.md) |
| Writing a checkpoint, artifact or human-facing output | [`writing`](references/writing.md)     |
