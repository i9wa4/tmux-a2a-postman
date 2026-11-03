---
name: postman-config-auditor
license: MIT
description: |
  USE FOR: Audit or fix Postman postman.md topology, Mermaid edges, native role
  bindings, reviewer selection, parent/child interfaces and recipient setup.
  DO NOT USE FOR: Generic CLI help or live mailbox handling; use postman-usage.
metadata:
  postman-skill-body: >-
    Read and follow `tmux-a2a-postman help postman-config-auditor`.
---

# postman-config-auditor

Use the installed `tmux-a2a-postman` executable and check its `version`. For
each matching row, in table order, run:

```sh
tmux-a2a-postman help postman-config-auditor <ref>
```

Read the complete output before acting. If this binary lacks the reference,
report the incompatibility; do not substitute another checkout or download.

| Read when                               | ref                            |
| --------------------------------------- | ------------------------------ |
| Always when auditing or fixing topology | [`guide`](references/guide.md) |
| Checking roles, routes and interfaces   | [`audit`](references/audit.md) |
