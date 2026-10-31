# File-first architecture

## 1. Workspace identity and storage

Each foreground CLI resolves its native tmux server socket/incarnation,
session and pane. The configured role is the unique live pane title in that
session. The session creation directory, canonicalized below HOME, supplies
the original workspace root; a later `cd` cannot redirect mail.

State lives below `${XDG_STATE_HOME:-$HOME/.local/state}/tmux-a2a-postman`:

```text
locks/<workspace>.lock
<workspace>/workspace.json
<workspace>/runtime/binding.json
<workspace>/runtime/bash-notifiers/<role>.json
<workspace>/agents/<role>/{draft,inbox,read,sent,failed}/
<workspace>/agents/<role>/read/<message-id>.pop.json
<workspace>/agents/<role>/executions/<operation-id>.json
<workspace>/agents/<role>/pane/
<workspace>/transactions/
```

Workspace names escape ambiguous path-component bytes before flattening the
HOME-relative path. `workspace.json` verifies the original canonical root.
A short nonblocking workspace lock protects each mutation and identity
refresh. A different live session cannot act as this workspace; only verified
stale ownership permits takeover without erasing history. Unknown ownership
fails explicitly. No legacy daemon lock or configuration gate is involved.

First mutation creates only needed private directories (0700) and files
(0600). `get-status` reads saved facts without initialization or capture.

## 2. Mail and exact replies

One send publishes independent receiver-inbox and sender-sent Markdown copies.
A minimal transaction record recovers interrupted multi-file publication.
The envelope is a one-line JSON HTML comment followed by literal Markdown.
POP claims only the caller's mail, moves it to its final read path, and writes
a claim receipt. Concurrent POP cannot claim it twice. Output failure never
makes a claimed message eligible again; read history is directly recoverable.
Read the complete archive through EOF before interpreting or replying.

A reply identifies the claimed original message. Required replies also fill
its exact input-request ID, sender and recipient. Generic ACKs and unrelated
sends do not discharge obligations. Mail-derived self-state separates unread
attention, inbound replies owed and outbound replies awaited.

## 3. Routing and activation

Local mail requires an explicit Mermaid edge. Cross-session mail requires a
declared direct parent/child relationship and the exact interface roles in
`workspace_tree`; both named native runtimes are verified at send time.
Directory ancestry and same-named local roles cannot authorize or redirect
mail. Each workspace retains independent ownership, inbox/read and sent files.

After durable publication, the same CLI makes a bounded attempt through the
recipient-owned [notification adapter](notification.md). Sending never waits
for POP, response, or work completion. A recipient handler can invoke Codex
noninteractively; no persistent Postman worker is introduced.

## 4. Commands and observations

[Command approval](../command-approvals.md) uses the same mail and notification
path. The synchronous wrapper consumes only its own exactly matched decision.
No mailbox lock spans approval waiting or Bash execution. Audit records never
become a resumable execution queue or reusable authority.

Optional pane observations are bounded and timestamped. Native positive
capture remains unsupported because mutable role titles cannot prove stable
attribution through a capture. Missing/stale evidence stays unknown. Saved
screen text never proves ongoing work or task completion.

Reusable compaction-marker detection remains isolated in `internal/idle`.
No observer, auto-PING, TUI, background delivery, daemon RPC, or daemon startup
surface is retained. Old histories are not migrated, deleted, or interpreted
as new command authority. Future observer work is a separate decision.
