# Session Flow

Use [postman-usage](../SKILL.md) for POP and complete-body reading, and
[postman-send-message](../../postman-send-message/SKILL.md) for sending.

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
identifier, required-input identifier, and authorized return route. Send an
exact resolving reply using supported metadata from current help. Inspect the
send result for exact closure; an unrelated send, POP, or generic ACK does not
fill it.

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
[postman-config-auditor](../../postman-config-auditor/SKILL.md). Do not repair
runtime files manually, broaden routes, kill unrelated sessions, or blindly
resend uncertain deliveries. A task-level BLOCKED report names the original
failing requirement, evidence, and remaining blocker.
