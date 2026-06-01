---
name: postman-session-operator
license: MIT
description: |
  USE FOR: MUST LOAD after sends or live mailbox/session work: passive waits,
  exact replies, bounded status, stale/dead-letter state, and pane hints.
  DO NOT USE FOR: first-contact sends, polling loops, or topology audits.
---

# postman-session-operator

MUST load after sending or live mailbox/session work; first contact uses
`postman-send-message`.

## 1. USE FOR / Procedure

1. Do not poll. After sends or delegated work, wait passively for notification,
   exact reply, timeout/watchdog boundary, explicit user status request, or
   concrete delivery trouble.
2. `pop` is only for explicit notification/current evidence of mail; never run
   repeated `pop` or sleep/pop loops.
3. Bounded status is only for explicit user status request, watchdog boundary,
   or concrete delivery trouble; never use status as a heartbeat.
4. `tmux-a2a-postman inspect-message --id <message_id>` is read-only
   historical lookup. Use `--path` for the stored Markdown path and `--body`
   for sender-authored body text.
5. After every successful `pop` with `status=message`, read the complete
   archived Markdown body before any handling, routing, reply, status decision,
   or no-action or no-op decision. `messageType: ping`, `replyPolicy: none`,
   and other metadata do not allow skipping the body. truncated command output
   does not count as a complete read.
6. Filling an input request closes transport, not task acceptance. After any
   exact reply, check send JSON `fill`, `required_input`, and `notice`.
   DONE/completion or BLOCKED/task-acceptance replies require
   `Task artifact: <artifact-reference>`, Original checklist: PASS, evidence,
   and Remaining blockers: none. Use `BLOCKED` with `Original checklist: FAIL`.
7. Receivers verify checklist status, durable references, evidence, and
   blockers before relaying, approving, or closing work.
8. Pane ids require exact tmux verification; missing panes are stale evidence.
   Dead letters, missing routes, or stale topology go to
   `postman-config-auditor`. More detail:
   [Session Flow](references/session-flow.md).
