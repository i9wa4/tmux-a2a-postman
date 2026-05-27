---
name: postman-session-operator
license: MIT
description: |
  USE FOR: Live mailbox/session operation, mail triage, exact replies, waits,
  blocked/stale/dead-letter state.
  DO NOT USE FOR: first contact or topology audits.
---

# postman-session-operator

**UTILITY SKILL**. INVOKES: status/mailbox commands.

## 1. USE FOR

- Interpret pending/waiting/blocked/stale/dead-letter state.
- Claim unread mail, read complete archives, close exact requests.
- Use `inspect-message --id <message_id>` as a read-only historical lookup.

## 2. Procedure

1. Use `get-status` for on-demand decisions, not polling. After delegated or
   reply-required mail, wait for pane notifications, exact replies,
   timeout/watchdog boundaries, or suspected delivery trouble.
2. After every successful `pop` with `status=message`, read the complete
   archived Markdown body before any handling, routing, reply, status decision,
   or no-action or no-op decision.
3. `messageType: ping`, `replyPolicy: none`, and other metadata do not allow
   skipping the body. truncated command output does not count as a complete
   read.
4. Filling an input request closes transport, not task acceptance. After exact
   replies, check send JSON `fill`, `required_input`, and `notice`; `DONE` still
   requires `Task artifact: <artifact-reference>`, `Original checklist: PASS`,
   evidence, and `Remaining blockers: none`. Use `BLOCKED` with
   `Original checklist: FAIL`; receivers should verify checklist status, durable
   references, evidence, and blockers before relaying, approving, or closing
   work.
5. Route dead letters, missing routes, or stale topology to
   `postman-config-auditor`. Details:
   [Session Flow](references/session-flow.md).

## 3. DO NOT USE FOR

- First contact; use `postman-send-message`.
- Config/topology/skill audits; use `postman-config-auditor`.
- Infrastructure management or daemon repair.

## 4. Troubleshooting

If status is stuck after route checks, send `BLOCKED`; do not edit mailbox
files.
