---
name: postman-config-auditor
license: MIT
description: |
  USE FOR: Audit tmux-a2a-postman config, postman.md topology and syntax, node
  templates, skill_path catalogs, and postman.md versus SKILL.md boundaries.
  Use when reviewing or fixing postman.toml, postman.md, nodes/*, Mermaid
  edges, interface_node, dead-letter routes, unread backlogs, skill catalog triggers,
  or node renames. DO NOT USE FOR: generic CLI help; run tmux-a2a-postman help.
---

# postman-config-auditor

Audit tmux-a2a-postman configuration with implementation-level accuracy.

For the file-first `send-heredoc`, `pop`, `get-status`, and `execute-bash`
adapters, audit the single absolute `postman.md` used by that invocation.
They read its bounded frontmatter `workspace_tree` for declared parent-child
interface routing, but do not load `postman.toml`, split node files, or daemon
policies. The linked references distinguish that declaration from legacy TOML
`workspace_tree` and daemon PING configuration.

**UTILITY SKILL**. INVOKES: local source inspection and direct file edits.

## 1. USE FOR

- Audit or fix `postman.toml`, `postman.md`, or `nodes/*` config.
- Check Mermaid edges, `interface_node`, role templates, and skill catalogs.
  `interface_node` has no default and `ui_node` is not an alias: an omitted
  `interface_node` means no interface node (fail closed), and any `ui_node`
  form is warned about and ignored.
- Diagnose `get-status` evidence for dead-letter, missing route, quiet node, or
  unread backlog symptoms.

## 2. Procedure

1. Read [the audit guide](references/audit-guide.md) before reporting or editing
   config, topology, templates, skill catalogs, or deployed config.
2. Read [the postman.md format reference](references/postman-md.md) before
   judging `postman.md` syntax or merge behavior.
3. For command syntax, prefer `tmux-a2a-postman help`.
4. If asked to fix the repository, edit the source files directly; if asked
   only to audit, return findings and concrete recommended patches.

## 3. DO NOT USE FOR

- Generic CLI usage questions.
- Live inbox/reply/session workflow operation, or first-contact message
  sending; use `postman-usage`.
