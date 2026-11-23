---
name: postman-recall
license: MIT
description: |
  USE FOR: Recover your own Postman conversation after restart or lost context,
  including what you received and sent in the latest archived day.
  DO NOT USE FOR: Resuming a known canonical Postman task record
  (postman-records), selecting an ordinary local Markdown tracker (logbook),
  summarizing the current chat, sending or replying (postman-send-message),
  handling new inbox notifications or current status (postman-usage), topology
  auditing (postman-config-auditor), or reading another agent's history.
metadata:
  postman-skill-body: >-
    Read and follow `tmux-a2a-postman help postman-recall`.
---

# postman-recall

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman help postman-recall <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                                   | ref                                    |
| ------------------------------------------- | -------------------------------------- |
| Always when restoring your own context      | [`guide`](references/guide.md)         |
| Selecting and reading the recorded mail day | [`procedure`](references/procedure.md) |
