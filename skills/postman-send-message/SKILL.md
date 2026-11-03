---
name: postman-send-message
license: MIT
description: |
  USE FOR: All ordinary Postman sends, including delegated work, exact replies,
  status requests, follow-ups, and terminal reports; preserve literal message
  bodies and handle delivery and notification outcomes. Reply to claimed message
  IDs with exact correlation and the authorized return route.
  DO NOT USE FOR: POP, passive waits, history, or status interpretation (use
  postman-usage); topology changes (use postman-config-auditor).
---

# postman-send-message

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman skills postman-send-message <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                                      | ref                                  |
| ---------------------------------------------- | ------------------------------------ |
| Sending a new request or exact reply           | [`sending`](references/sending.md)   |
| Interpreting delivery or notification outcomes | [`outcomes`](references/outcomes.md) |
| Waiting or checking outstanding obligations    | [`waiting`](references/waiting.md)   |
