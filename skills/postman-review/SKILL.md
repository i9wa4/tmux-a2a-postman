---
name: postman-review
license: MIT
description: |
  USE FOR: Review an artifact with guardian and independent critic evidence.
  DO NOT USE FOR: Approve a typed shell execution request without substantive artifact review.
---

# postman-review

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman skills postman-review <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                                     | ref                                    |
| --------------------------------------------- | -------------------------------------- |
| Choosing perspectives and assigning reviewers | [`routing`](references/routing.md)     |
| Preparing a blind reviewer packet             | [`packet`](references/packet.md)       |
| Recording batches and converging results      | [`ledger`](references/ledger.md)       |
| Handling blockers, rejection or rework        | [`recovery`](references/recovery.md)   |
| Always before substantive artifact review     | [`gates`](references/gates.md)         |
| Checking regression scenarios                 | [`scenarios`](references/scenarios.md) |
| Selecting an offline perspective prompt       | [`prompts`](references/prompts.md)     |
| Returning structured reviewer findings        | [`report`](references/report.md)       |
| Selecting bounded read-only checks            | [`lookup`](references/lookup.md)       |
