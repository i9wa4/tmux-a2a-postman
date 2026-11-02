# postman-recall

Recover your own last active day's working context from durable mail. This is
a read-only workflow, not a memory database, task tracker or live activity test.

## 1. Verify your scope

Run `tmux-a2a-postman get-status` in the explicit server context. The getter
reads a saved whole-server snapshot; `--config` does not select its membership.
Select the assigned session/native pane/workspace before reading own archives.
Use matching installed help; a getter does not authenticate the current native
caller. It returns dated observations, not a top-level workspace or self node.
Resolve your logical node from the exact saved/native binding as described in
`tmux-a2a-postman help recall-procedure`. Do not guess from cwd or pane title.
Missing, changed or ambiguous identity stops recall with an actionable unknown.
Do not POP to manufacture identity during recall.

## 2. Select and read the recorded day

1. Inspect metadata from **your read and sent together**, excluding receipts,
   inbox, draft, failed and other agents. Use archived `created_at`, never
   mtime.
2. Convert timestamps to UTC. Select the UTC calendar date of the newest valid
   message, even if it is Friday and today is Monday. Invalid/missing metadata
   prevents claiming that a day is definitively the latest; report the affected
   files instead of silently skipping them.
3. Read every selected message's **complete Markdown through EOF**, in exact
   chronological order across both directories. Continue numbered chunks until
   all lines are read. Metadata indexing, notification text, paths and previews
   do not count as reading the messages.
4. Follow exact older `reply_to` / `replies[].message_id` references only when
   needed to understand this day's exchange. Stay within your own read/sent;
   report unavailable context rather than opening raw inbox or foreign mail.

`tmux-a2a-postman help recall-procedure` gives concrete identity checks,
layout, quoting, nanosecond tie rules, examples and a pure metadata selector.

## 3. Reconstruct and stop

Report the original task/instructions, decisions, last confirmed work with
evidence, outstanding questions/obligations, and the next concrete action.
Include message IDs/file references and distinguish confirmed facts, historical
claims and missing context. Empty history means no archived context recovered.

Read current file-derived status separately: old narrative, a POP receipt, a
reply or a sampled pane screen does not prove current task completion/activity.
Use `tmux-a2a-postman help usage` for known unread mail and normal
POP/full reading after recall; use
`tmux-a2a-postman help send-heredoc` for an authorized send.
Recall itself sends nothing, claims nothing, rewrites nothing, starts no daemon
and never polls empty status/POP in a loop.
