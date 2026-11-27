# Messaging syntax

New request:

Choose a fresh delimiter absent as a complete body line for each send; see
`tmux-a2a-postman help send-heredoc`.

```sh
tmux-a2a-postman send-heredoc --to worker <<'POSTMAN_BODY_7f4c39b2'
Literal Markdown body.
POSTMAN_BODY_7f4c39b2
```

Reply to an original reply-required request after POP and a full archive read:

```sh
tmux-a2a-postman send-heredoc --to <actual-POP-request-id> <<'POSTMAN_BODY_a9d2e806'
Response.
POSTMAN_BODY_a9d2e806
```

An exact reply derives its return route and creates no new obligation. An
ordinary reply's ID cannot be a reply target; continue with a fresh request to
a configured role. See help send-heredoc for combined replies, output and
notification semantics.
