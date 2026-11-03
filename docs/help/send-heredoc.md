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

## 1. Send

1. Resolve the sender and intended recipient against the current workspace,
   verified binding, configured edges, and return route. A footer is a hint;
   it does not authorize a route or override a role contract.
2. Use quoted heredoc stdin; keep Markdown headings and literal body text.

   ```sh
   tmux-a2a-postman send-heredoc --to worker <<'POSTMAN_BODY'
   Message text, including literal $HOME and `code`.
   POSTMAN_BODY
   ```

3. Address a configured node for a new request; every new request requires a
   reply. Address your actual POP'd message ID to reply. The tool recovers its
   exact correlation and authorized return route; never copy another request's
   ID because its text looks similar. The generated archive footer shows the
   exact command. A reply does not open another reply obligation.
4. Combine IDs only when they are all your claimed mail from the same sender
   and return route, and one response actually resolves all of them. Otherwise
   reply separately. Consult installed help for syntax and limits.

## 2. Read the result

The foreground command publishes an independent receiver inbox copy and sender
sent copy. Read its delivery outcome, notification outcome, and mandatory
post-operation self-state separately. Notification failure does not undo a
committed delivery. A failed or uncertain invocation is not permission for a
blind resend; retain its identifiers and inspect the recorded operation.

The foreground sender pastes the notification into the existing recipient
agent's tmux input and submits it with `C-m`. This transport is shared by all
agent runtimes, including the sender receiving a reply and the command reviewer.
No notifier registration, model-specific API or Postman daemon is needed. A
normally accepted notification is omitted from JSON. A separate `notification`
reports uncertainty or unavailability, never POP or response. Unavailable
targets report `unavailable`; an ambiguous attempt reports `unknown` and is
never replayed. Delivery remains committed in both cases.

Unrelated sends and POP do not close required replies. Use file-derived
inbound/outbound counts to assess remaining obligations; missing state is
unknown. Read the archives when exact historical IDs matter.

## 3. After sending

Wait passively for a genuine notification or exact reply. Do not use repeated
POP, sleep/POP, or status loops as a wait mechanism. Use
`tmux-a2a-postman help usage` and its
`tmux-a2a-postman help session-flow` for bounded status,
known mail, stale evidence, and escalation.

For cross-session mail use `session:node` only across a declared direct
parent/child interface route with verified native peers. Reply to the actual POP
message ID; its saved return route is selected automatically. Do not infer
eligibility from directory ancestry or matching local node names.
