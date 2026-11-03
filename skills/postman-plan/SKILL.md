---
name: postman-plan
license: MIT
description: |
  USE FOR: Plan a multi-step change and investigate its reproducible failure.
  DO NOT USE FOR: Send an already written exact reply with no planning work.
---

# postman-plan

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman skills postman-plan <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                                             | ref                                            |
| ----------------------------------------------------- | ---------------------------------------------- |
| Understanding the request, investigating or debugging | [`investigation`](references/investigation.md) |
| Writing, updating or handing off a plan               | [`planning`](references/planning.md)           |
