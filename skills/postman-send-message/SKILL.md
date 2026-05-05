---
name: postman-send-message
license: MIT
description: |
  Send the first message to another node using tmux-a2a-postman send.
  Use when:
  - The user asks to send a message to another agent node
  - An agent needs to make initial contact and may not know the command exists
  Do not use for daemon management or reading inbox messages.
---

# postman-send-message

Send one message to a recipient node in the current tmux-a2a-postman session.
Use quoted heredoc stdin as the default non-interactive agent-safe form:

```sh
tmux-a2a-postman send --to <node> <<'POSTMAN_BODY'
message text
POSTMAN_BODY
```

The single quotes around `POSTMAN_BODY` are required when the body may contain
command substitutions, backticks, `$HOME` variables, quotes, or shell examples.
For generated files, use `--body-file`:

```sh
tmux-a2a-postman send --to <node> --body-file path/to/body.md
```

`--body-stdin` is available for explicit stdin or pipe workflows. Direct
`--body` is legacy simple-literal input and should not be used for agent
messages.

The sender is auto-detected from the current tmux pane title. Use
`tmux-a2a-postman help send` for details.
