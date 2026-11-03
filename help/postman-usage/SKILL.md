---
name: postman-usage
license: MIT
description: |
  USE FOR: Postman mailbox and session work: genuine notifications, POP and
  complete archived-body reads, exact replies, passive waits,
  bounded file-based status, pane evidence, and execute-heredoc orientation.
  DO NOT USE FOR: Ordinary sending procedure (use postman-send-message),
  configuration/topology audit (use postman-config-auditor), or deciding
  execute-heredoc approval as the approver, or restart/compaction recovery
  (use postman-recall).
metadata:
  postman-skill-body: >-
    Read and follow `tmux-a2a-postman help postman-usage`.
---

# postman-usage

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman help postman-usage <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                                       | ref                                          |
| ----------------------------------------------- | -------------------------------------------- |
| Handling a notification or claiming unread mail | [`mailbox`](references/mailbox.md)           |
| Sending, replying or waiting after a send       | [`sending`](references/sending.md)           |
| Inspecting state or command-execution evidence  | [`status`](references/status.md)             |
| Reconciling obligations, follow-ups or latency  | [`session-flow`](references/session-flow.md) |
| Handling external-channel mail                  | [`external`](references/external.md)         |
| Making a first configured exchange              | [`quickstart`](references/quickstart.md)     |
