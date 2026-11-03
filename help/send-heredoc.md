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

The sender pastes one short hint, waits at least 600ms, then sends C-m. It
compares the whole capture-pane immediately before Enter and at least 600ms
after it. Up to four inserted/deleted lines is a small change: after the same
minimum interval it retries Enter, at most three presses total. A substantial
change stops extra Enter. Retry cooldown grows by 300ms per retry, making Enter
intervals at least 600ms, 900ms and 1200ms from the preceding paste/key. This
breaks repeated interleaving between independent senders. A newline or viewport
scroll is counted as a line edit, not a change at every later row. There are no
prompt, cursor/input-area, Codex/Claude or other provider rules. Screen change
is a retry heuristic and cannot prove submission, POP, understanding or a
response. An exhausted or unobservable attempt reports unknown, leaving durable
mail intact.

There is no destination notification lock or persistent input guard. Independent
senders may paste into one terminal; each uses the same bounded observation
procedure, and the last sender can submit the collected hints. Message-ID
attempt records still prevent re-pasting the same uncertain message. Old guard
and lock files remain untouched and cannot block fresh messages. Short mailbox
transaction/claim locks remain. Human drafts and unrelated writers are not
exclusively controlled. Shell prompts, tmux input modes and changed native
ownership/geometry stop input. Notification input has a five-second budget;
total post-commit work remains bounded to eight seconds. A separate bounded
session snapshot never changes delivery success. Send does not wait for a reply.

For the operating procedure, run `tmux-a2a-postman help postman-send-message`
and read the matching references.
