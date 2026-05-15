---
name: postman-session-operator
license: MIT
description: |
  USE FOR: Operate live tmux-a2a-postman sessions with existing mail,
  get-status state, exact replies, waits, blocked/stale/dead-letter signals.
  DO NOT USE FOR: first contact or topology audits.
---

# postman-session-operator

Operate existing message workflows.

**UTILITY SKILL**. INVOKES: live status inspection and mailbox commands.

## USE FOR

- Interpret status, pending, waiting, blocked, stale, and dead-letter signals.
- Claim unread mail, read complete archives, and close exact input requests.
- Use `tmux-a2a-postman inspect-message --id <message_id>` as a read-only
  historical lookup. Use `--path` for the stored Markdown path and `--body` for
  sender-authored body text.

## Procedure

1. Use `tmux-a2a-postman get-status`.
2. After every successful `pop` with `status=message`, read the complete
   archived Markdown body before any handling, routing, reply, status decision,
   or no-action or no-op decision.
3. `messageType: ping`, `replyPolicy: none`, and other metadata do not allow
   skipping the body. truncated command output does not count as a complete
   read.
4. Filling an input request closes transport, not task acceptance. Include
   `Task artifact: <artifact-reference>`, `Original checklist: PASS`, and
   `Remaining blockers: none`. Use `BLOCKED` with `Original checklist: FAIL`;
   receivers should verify checklist status, durable references, evidence, and
   blockers before relaying, approving, or closing work.
5. Treat dead letters, missing routes, or stale topology as
   `postman-config-auditor` work. Details:
   [Session Flow](references/session-flow.md).

## DO NOT USE FOR

- First-contact message sending; use `postman-send-message`.
- Config, topology, or skill catalog audits; use `postman-config-auditor`.
- Infrastructure management or low-level daemon repair.

## Troubleshooting

If status remains unavailable or delivery stays stuck after route checks, send
`BLOCKED` to the operator instead of manually editing mailbox files.
