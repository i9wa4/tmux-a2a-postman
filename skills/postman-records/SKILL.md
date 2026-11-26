---
name: postman-records
license: MIT
description: |
  USE FOR: Maintaining or refreshing local work records and shared
  decisions, or recalling prior work across delegation, resumption
  and handoffs.
  DO NOT USE FOR: New-mail intake or mailbox-status checks alone.
---

# Postman records

Use ordinary local Markdown for context that must survive a handoff or restart.
Start with the current conversation and trusted active record. Do not create a
large log for every task or another registry. Select one verified canonical
record for each owning scope; recover a known unreadable record rather than
silently replacing it. Preserve original requests, accepted changes, criteria,
authority, pending work, decisions and applicable evidence.

Read [records](references/records.md) when selecting, writing or explicitly
refreshing a management record or shared decision file. Read
[recall](references/recall.md) when reconstructing your own earlier Postman
exchange after lost context. Recall is read-only and does not POP or send. If
the current conversation and trusted record suffice, no historical search is
needed. In a configured team, only the designated tracking owner writes the
complete scoped task record; other roles may keep useful notes or reports. The
default team assigns that owner to each orchestrator for its own layer.

For portable local Markdown creation, the bundled `scripts/mkmd` helper retains
the existing root convention. The `scripts/select_day.py` helper selects a UTC
recorded day from already verified own-archive metadata; it does not establish
native identity. Materialize helpers only when needed with
`tmux-a2a-postman resources` and use the returned bundle path. The binary's
Skill and references can be read without materialization.
