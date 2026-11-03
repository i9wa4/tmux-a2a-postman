# send-heredoc — publish mail and attempt a bounded input notification

```sh
tmux-a2a-postman send-heredoc --to worker <<'POSTMAN_BODY'
Literal Markdown, including $HOME and `code`.
POSTMAN_BODY
```

Required: --to <node|session:interface|POP'd-message-id[,POP'd-message-id...]>.
Optional: `--config <absolute-path>`. No positional arguments or reply flags.
The nonterminal stdin body is 1–61440 bytes; JSON escaping and recovery metadata
must also fit the durable-record limit, so some bodies have a smaller limit.

Node addresses create requests requiring a reply. A POP'd ID automatically
selects its sender and exact return route; a reply creates no new obligation.
At most 16 different POP'd IDs with one sender/return route can receive one
combined reply. Validate the whole set before delivery. Archives append a
versioned reply-instruction section without altering the literal user body.

JSON: workspace, as_of, success, self; optional reason, recovery_message_id,
notification. A normal accepted notification is omitted. success means durable
receiver-inbox and sender-sent copies; it does not mean POP or task completion.
Confirmed non-delivery retains full failed mail; uncertainty retains a draft
and recovery identity. Do not blindly resend an uncertain operation.

A destination file lock serializes Postman writers. Generic capture observations
check settled text and the input area after exactly one C-m; no model names or
prompt glyph rules are used. Ambiguity retains a pending input guard and blocks
further paste/Enter until safely observable. Shell prompts and tmux input modes
are rejected. Human drafts/other input writers are not exclusively controlled.
Notification failure leaves committed mail intact. A bounded session snapshot
also runs; it never changes delivery success. Send does not wait for a reply.

Notification lock admission waits at most 5 seconds. After acquisition, input
settlement/submit observation gets up to 2 seconds; total post-commit work is
bounded to 8 seconds. Approval waits never hold the notification lock.

For the operating procedure, run `tmux-a2a-postman help postman-send-message`
and read the matching references.
