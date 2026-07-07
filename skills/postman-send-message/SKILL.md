---
name: postman-send-message
license: MIT
description: |
  USE FOR: MUST LOAD before first-contact or initial send-heredoc messages;
  preserves body text and blocks post-send polling.
  DO NOT USE FOR: waiting, polling, inbox reads, session work, or status after a
  send.
---

# postman-send-message

MUST load before any first-contact or initial node send. This is the pre-send
skill, not a waiting skill.

## 1. Procedure

1. Use quoted heredoc stdin:

   ```sh
   tmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'
   message text
   POSTMAN_BODY
   ```

2. Do not pass message text as a CLI argument, file-body shortcut, or generic
   pipe-oriented body.
   When workspace tree hierarchy is configured, `<node>` may also be a tree
   alias such as `@parent`, `@parent/orchestrator`, or `@child/api`.
3. Leave Markdown headings unchanged.
4. The sender is auto-detected from the current tmux pane title. Use
   `tmux-a2a-postman help send-heredoc` for details.
5. After a terminal/informational send, stop. After a reply-required send or
   any live mailbox/session decision, load
   `postman-session-operator`.

## 2. After Send

After a successful send:

| Case                               | Action                                                             |
| ---------------------------------- | ------------------------------------------------------------------ |
| Informational or terminal send     | Stop.                                                              |
| Reply-required send                | Wait for daemon notification or exact reply.                       |
| Timeout/watchdog boundary          | Use `postman-session-operator`; inspect daemon-submit request ids. |
| Suspected delivery/routing trouble | Use `postman-session-operator`.                                    |

`pop` must not be used as a wait or poll mechanism after a successful send.
Forbidden post-send wait patterns: repeated `pop`, `sleep && pop`, and mixed
`pop`/`get-status` loops.

Only use `pop` for explicit notification/current evidence of mail. Bounded
status is only for explicit user status request, watchdog boundary, or concrete
delivery trouble. See
`skills/postman-session-operator/references/session-flow.md` for `waiting` and
`expected_wait` handling. Use
`tmux-a2a-postman inspect-daemon-submit --id <request_id>`.
