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

Send one message to a recipient node in the current tmux-a2a-postman session:

```sh
tmux-a2a-postman send --to <node> --body 'message text'
```

For shell-sensitive or multiline text containing command substitutions,
backticks, `$HOME` variables, quotes, or shell examples, use a file or standard
input instead of a direct `--body` argument:

```sh
tmux-a2a-postman send --to <node> --body-file path/to/body.md
tmux-a2a-postman send --to <node> --body-stdin < path/to/body.md
```

The sender is auto-detected from the current tmux pane title. Use
`tmux-a2a-postman help send` for details.
