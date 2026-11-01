---
name: postman-config-auditor
license: MIT
description: |
  USE FOR: Audit or fix Postman postman.md topology, Mermaid edges, native role
  bindings, reviewer selection, parent/child interfaces and recipient setup.
  DO NOT USE FOR: Generic CLI help or live mailbox handling; use postman-usage.
---

# postman-config-auditor

## 1. Audit current configuration

Read [the audit guide](references/audit-guide.md) and
[format reference](references/postman-md.md). Inspect the single absolute
`postman.md` used by the invocation, its Mermaid edges and native role bindings.
Check direct parent/child interface declarations and recipient runtime
registration. No daemon configuration, TOML overlays or migration gate is
involved.

## 2. Apply requested fixes

For a fix request, edit configuration source directly within authorized scope.
For audit-only work, report findings with concrete patches. Do not edit runtime
mailboxes, broaden routes or treat missing evidence as verified absence.
Use installed `tmux-a2a-postman help` for actual commands and flags.

For POP/full reading, replies and passive waits use
[postman-usage](../postman-usage/SKILL.md); for ordinary sends use
[postman-send-message](../postman-send-message/SKILL.md).
