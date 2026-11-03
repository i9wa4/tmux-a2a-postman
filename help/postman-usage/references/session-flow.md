# Session Flow

Use `tmux-a2a-postman help postman-usage mailbox` for POP and complete-body
reading, and `tmux-a2a-postman help send-heredoc` for sending.

## 1. Choose an action from current evidence

- Genuine notification or current own self-state showing unread mail: POP,
  then read the entire returned archive before handling it.
- Explicit status request, concrete delivery trouble, or an active workflow
  follow-up boundary: one bounded get-status read and a scoped decision.
- An unresolved outbound request with no new trigger: wait passively.

No repeated POP, sleep/POP, mixed status/POP loops, startup PING ritual, or
background monitoring is required. Without an active caller or observer, no
automatic timeout follow-up occurs.

## 2. Preserve exact obligations

For each required request retain the original sender, recipient, message
identifier, required-input identifier, and authorized return route. Reply to the
actual POP message ID; the tool derives correlation and the return route.
Inspect remaining inbound/outbound counts and relevant archives; an unrelated
send, POP, or generic ACK does not fill it.

Each relay answers its own source request. A question to a different node
opens a separate exchange and leaves the original obligation unresolved.
Same-named nodes in other workspaces are different recipients.

Delivered, claimed, read, understood, replied, reviewed, accepted, and human
approved are different facts owned by different actors. Filling transport
does not accept work or authorize an otherwise forbidden action.

## 3. Read saved evidence honestly

Status reflects saved mail facts and timestamped observations. Separate unread
attention, replies owed, replies awaited, delivery failure, and observation
freshness. Missing, failed, ambiguous, or stale evidence stays unknown.

A pane snapshot records a bounded observation, its time, and target identity.
A notification or POP changing the screen does not prove task progress.
Repeated equal snapshots do not prove continuous inactivity. A missing child
workspace or stale interface observation is not healthy completion.

## 4. Follow up and escalate

At an authorized follow-up boundary, send one ordinary status-request message
over the allowed route. Lack of an answer alone does not prove a dead process.
Report the precise unknown fact and durable evidence; do not invent a
timeout verdict or task result.

Route topology, recipient, interface, ownership, or configuration issues to
`tmux-a2a-postman help postman-config-auditor guide`. Do not repair
runtime files manually, broaden routes, kill unrelated sessions, or blindly
resend uncertain deliveries. A task-level BLOCKED report names the original
failing requirement, evidence, and remaining blocker.

## 5. Investigate latency when requested

For a concrete delivery/performance problem, inspect only your own authorized
per-invocation logs under `agents/<node>/logs/<UTC-start-date>/`. Use the
`tmux-a2a-postman help invocation-diagnostics` for schema,
read-only analysis and exact existing message/notice/receipt/audit joins.
Compare equivalent builds/configuration/workloads, including failed and
incomplete samples. Missing end events or old dates do not authorize replay,
prove a live process, or establish task state. Logs carry no mail bodies or
child streams and remain until the user deletes them. No log scan belongs in
normal status display or passive waiting.
