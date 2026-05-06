---
name: postman-send-message
license: MIT
description: |
  Send the first message to another node using tmux-a2a-postman send-heredoc.
  Use when:
  - The user asks to send a message to another agent node
  - An agent needs to make initial contact and may not know the command exists
  Do not use for daemon management or reading inbox messages.
---

# postman-send-message

Send one message to a recipient node in the current tmux-a2a-postman session.
Use the heredoc-explicit command with a quoted delimiter:

```sh
tmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'
message text
POSTMAN_BODY
```

The single quotes around `POSTMAN_BODY` are required when the body may contain
command substitutions, backticks, `$HOME` variables, quotes, code fences, or
shell examples. Do not pass message text as a CLI argument, file-body shortcut,
or generic pipe-oriented body.

The sender is auto-detected from the current tmux pane title. Use
`tmux-a2a-postman help send-heredoc` for details.
