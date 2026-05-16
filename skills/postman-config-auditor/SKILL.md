---
name: postman-config-auditor
license: MIT
description: |
  USE FOR: Audit tmux-a2a-postman config, postman.md topology and syntax, node
  templates, skill_path catalogs, and postman.md versus SKILL.md boundaries.
  Use when reviewing or fixing postman.toml, postman.md, nodes/*, Mermaid
  edges, ui_node, dead-letter routes, unread backlogs, skill catalog triggers,
  or node renames. DO NOT USE FOR: generic CLI help; run tmux-a2a-postman help.
---

# postman-config-auditor

Audit tmux-a2a-postman configuration with implementation-level accuracy.

**UTILITY SKILL**. INVOKES: local source inspection and direct file edits.

## USE FOR

- Audit or fix `postman.toml`, `postman.md`, or `nodes/*` config.
- Check Mermaid edges, `ui_node`, role templates, and skill catalogs.
- Diagnose `get-status` evidence for dead-letter, missing route, quiet node, or
  unread backlog symptoms.

## Procedure

1. Read [the audit guide](references/audit-guide.md) before reporting or editing
   config, topology, templates, skill catalogs, or deployed config.
2. Read [the postman.md format reference](references/postman-md.md) before
   judging `postman.md` syntax or merge behavior.
3. For command syntax, prefer `tmux-a2a-postman help`.
4. If asked to fix the repository, edit the source files directly; if asked
   only to audit, return findings and concrete recommended patches.

## DO NOT USE FOR

- Generic CLI usage questions.
- Live inbox/reply/session workflow operation; use `postman-session-operator`.
- First-contact message sending; use `postman-send-message`.
