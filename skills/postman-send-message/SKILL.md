---
name: postman-send-message
license: MIT
description: |
  USE FOR: Send the first message to another node using tmux-a2a-postman
  send-heredoc.
  Use when the user asks to send a message to another agent node or an agent
  needs initial-contact command guidance.
  DO NOT USE FOR: session operation, infrastructure management, or reading
  inbox messages.
---

# postman-send-message

Send first-contact messages to another node.

**UTILITY SKILL**. INVOKES: `tmux-a2a-postman send-heredoc`.

## USE FOR

- Send an initial message to another configured node.
- Preserve heredoc body text exactly.

## Procedure

1. Use quoted heredoc stdin:

   ```sh
   tmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'
   message text
   POSTMAN_BODY
   ```

2. Do not pass message text as a CLI argument, file-body shortcut, or generic
   pipe-oriented body.
3. Leave Markdown headings unchanged; stored transport/header guidance is added
   before a visible separator.
4. The sender is auto-detected from the current tmux pane title. Use
   `tmux-a2a-postman help send-heredoc` for details.
5. After a successful send, do not start a continuous polling loop. The daemon
   notifies the recipient pane; use status only for explicit status requests,
   timeout/watchdog boundaries, or suspected delivery failure.

## DO NOT USE FOR

- Inbox reads, reply-required closure, or status decisions; use
  `postman-session-operator`.
- Topology or route problems; use `postman-config-auditor`.
- Infrastructure management or low-level repair.

## Troubleshooting

If delivery fails, trust the command error over stale footer text and audit the
current route before retrying.
