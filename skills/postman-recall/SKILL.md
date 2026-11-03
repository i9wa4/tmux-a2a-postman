---
name: postman-recall
license: MIT
description: |
  USE FOR: Restore your own Postman context after restart or compaction;
  explicitly recall the latest recorded day's read and sent correspondence.
  DO NOT USE FOR: Sending or replying (postman-send-message), handling new
  inbox notifications or current status (postman-usage), topology auditing
  (postman-config-auditor), or reading another agent's history.
---

# postman-recall

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman skills postman-recall <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                                   | ref                                    |
| ------------------------------------------- | -------------------------------------- |
| Always when restoring your own context      | [`guide`](references/guide.md)         |
| Selecting and reading the recorded mail day | [`procedure`](references/procedure.md) |
