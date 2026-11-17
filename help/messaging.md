# Messaging syntax

New request:

```sh
tmux-a2a-postman send-heredoc --to worker <<'POSTMAN_BODY'
Literal Markdown body.
POSTMAN_BODY
```

Reply after POP and a full archive read:

```sh
tmux-a2a-postman send-heredoc --to <actual-POP-message-id> <<'POSTMAN_BODY'
Response.
POSTMAN_BODY
```

A reply derives its exact return route and creates no new obligation. See
`help postman-usage mailbox` for POP and complete archive reads,
`help postman-send-message sending` for ordinary sends and exact replies,
`help send-heredoc` for syntax and combined-reply limits, and
`help postman-send-message outcomes` for delivery, notification and self-state.
