# Recipient notification

## 1. Original pane-input workflow

Notifications are independent of the agent/model runtime. After durable mail
commits, the foreground CLI finds the verified recipient tmux pane, pastes a
mail hint into its existing input editor and submits it with `C-m`. The agent
POPs the notified ID, reads the complete archive and sends any exact reply.
Codex, Claude Code and local LLM consoles use the same path.

This preserves the original `set-buffer` → `paste-buffer` → `C-m` behavior.
The greeting, `You've got mail:` filename hint and HTML protocol sentinels are
retained. The hint is one line for compatibility with plain line-oriented input;
it includes the exact POP ID and full-read instruction, never the mail body.
There is no model API, thread ID, notifier registration or Readline hook.
Existing agents are started by the user's launcher, such as vde-layout.

A unique named buffer prevents collisions with unrelated tmux buffers. One
command list creates the buffer, pastes it with bracketed-paste support when
the terminal enables it, deletes the consumed buffer, waits 150 ms for the UI,
and sends a real `C-m`. A separate per-recipient file lock serializes Postman
writers. No mailbox lock spans terminal input. Failed attempts clean up only
their own buffer and never repeat a paste or Enter on an uncertain outcome.

## 2. Binding, commit and evidence

Before input, the CLI verifies its exact committed sent copy, authorized
same-session or direct parent/child destination and current native target
binding. It rechecks server/session/pane/PID and role before sending. Native
identity and ownership checks remain; model-specific gates do not apply.
An ordinary shell prompt or tmux input/copy mode is rejected before paste.
A fresh two-second post-commit budget bounds notification work independently
of sender cancellation. Sending never waits for POP, a reply or completion.

The source reserves `runtime/notices/<id>.json` as `unknown` immediately before
the attempt and marks it accepted after tmux success. Concurrent/repeated
attempts for the same committed message never submit twice. A pre-paste
rejection does not reserve an attempt, so the exact committed message can be
notified later. Ambiguous input or receipt persistence remains unknown and is
never replayed. This is bounded foreground evidence, not a background
delivery/retry queue.

| Outcome              | Evidence                                                 |
| -------------------- | -------------------------------------------------------- |
| Durable delivery     | Independent sent and inbox/read files                    |
| `accepted` notice    | tmux accepted paste and C-m                              |
| `unavailable` notice | Binding/target/input could not be admitted before paste  |
| `unknown` notice     | Paste/submit or receipt persistence may be ambiguous     |
| POP claim            | Final read archive and `.pop.json` receipt               |
| Full read            | Recipient evidence such as a complete-file SHA256 digest |
| Response             | Exactly correlated reply and claim receipt               |

Acceptance does not prove POP, full reading, understanding or response. Failed
notifications leave durable unread mail and never undo a committed send or
permit an approval command. Known unread self-state is an explicit POP trigger;
idle polling and automatic replay are unnecessary.

## 3. Agent input contract

Receiving panes must host agents accepting pasted text and `C-m`, with stable
configured role titles. The agent controls its model access, tool permissions,
input handling while busy, and draft editing. As with the original transport,
Postman writes into the current composer; it does not snapshot/restore human
drafts or infer an empty composer from screen captures. The per-recipient lock
serializes Postman writers, not human input or arbitrary external key senders.
These limits apply uniformly to every agent type.

## 4. Verification

`nix run .#notification-smoke -- --case all` uses real tmux and deterministic
terminal agents without any AI model or runtime API. Both canonical line input
and raw bracketed-paste input receive the actual notification bytes and submit
key. Only that submitted input triggers the agent's POP/read/reply helper.
The sixteen-case matrix covers rally, busy, concurrent arrivals, parent/child
both directions, approval/rejection and blocked notification. Conversation
context, exact IDs, archive hashes, distinct claims and bounded sends are
verified. Copy-mode blocking and old-main controls commit mail but produce no
receiver action. The controller never POPs or replies.

An additional eight-case real Codex TUI matrix uses fixed local model responses,
matching vde-layout's launch, through the same tmux input path. It exercises no
Codex app-server/queue adapter. Claude/local-console compatibility rests on this
common terminal protocol; terminal fixtures do not claim a paid Claude/model
run. Go tests cover transport ambiguity, no replay, concurrency, lock
independence and shell input rejection. See
[Contributing](../../CONTRIBUTING.md).
