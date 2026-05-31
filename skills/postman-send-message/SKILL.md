---
name: postman-send-message
license: MIT
description: |
  USE FOR: First-contact node messages with send-heredoc while preserving body
  text.
  DO NOT USE FOR: session operation, inbox reads, or status decisions.
---

# postman-send-message

Send first-contact node messages.

**UTILITY SKILL**. INVOKES: `tmux-a2a-postman send-heredoc`.

## 1. USE FOR

- Send an initial message to another configured node.
- Preserve heredoc body text exactly.

## 2. Procedure

1. Use quoted heredoc stdin:

   ```sh
   tmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'
   message text
   POSTMAN_BODY
   ```

2. Do not pass message text as a CLI argument, file-body shortcut, or generic
   pipe-oriented body.
3. Leave Markdown headings unchanged.
4. The sender is auto-detected from the current tmux pane title. Use
   `tmux-a2a-postman help send-heredoc` for details.
5. After a successful send:

| Case                               | Action                                                             |
| ---------------------------------- | ------------------------------------------------------------------ |
| Informational or terminal send     | Stop.                                                              |
| Reply-required send                | Wait for daemon notification or exact reply.                       |
| Timeout/watchdog boundary          | Use `postman-session-operator`; inspect daemon-submit request ids. |
| Suspected delivery/routing trouble | Use `postman-session-operator`.                                    |

   `pop` must not be used as a wait or poll mechanism after a successful send.
   Forbidden post-send wait patterns: repeated `pop`, `sleep && pop`, and
   mixed `pop`/`get-status` loops. Mailbox/session decisions belong to
   `postman-session-operator`; see
   `skills/postman-session-operator/references/session-flow.md` for `waiting`
   and `expected_wait` handling. When a daemon-submit timeout reports a request
   id, use `tmux-a2a-postman inspect-daemon-submit --id <request_id>` before
   deciding whether a resend or follow-up is needed.

## 3. DO NOT USE FOR

- Inbox reads, reply-required closure, or status decisions; use
  `postman-session-operator`.
- Topology or route problems; use `postman-config-auditor`.
- Infrastructure management or low-level repair.

## 4. Troubleshooting

If delivery fails, trust the command error over stale footer text and audit the
current route before retrying.
