# File-first architecture

## 1. Workspace identity and storage

Each foreground CLI resolves its native tmux server socket/incarnation,
session and pane. The configured role is the unique live pane title in that
workspace. The verified native pane's current directory, canonicalized below
HOME, supplies the workspace root. The CLI subprocess cwd and the session's old
creation directory do not select it. An agent launched after navigation uses
its actual directory regardless of launcher; tool subprocess cd leaves it
unchanged. A role present in another directory is neither a local binding nor
verified absent, so it cannot silently remove a configured approval gate.
Native identity and ownership are still required; cwd alone grants no identity.

State lives below `${XDG_STATE_HOME:-$HOME/.local/state}/tmux-a2a-postman`:

```text
locks/<workspace>.lock
<workspace>/workspace.json
<workspace>/runtime/binding.json
<workspace>/runtime/notice-locks/<role>.lock
<workspace>/runtime/notices/<message-id>.json
<workspace>/runtime/input-guards/<role>.json
<workspace>/agents/<role>/{draft,inbox,read,sent,failed}/
<workspace>/agents/<role>/read/<message-id>.pop.json
<workspace>/agents/<role>/executions/<operation-id>.json
<workspace>/runtime/observations/
<workspace>/transactions/
```

Workspace names escape ambiguous path-component bytes before flattening the
HOME-relative path. `workspace.json` verifies the original canonical root.
A short nonblocking workspace lock protects each mutation and identity
refresh. A different live session cannot act as this workspace; only verified
stale ownership permits takeover without erasing history. Unknown ownership
fails explicitly. No legacy daemon lock or configuration gate is involved.

An existing application state root may remain 0755. It must be an owned real
directory without group/other write access; Postman does not chmod it.
First mutation creates only needed private child directories (0700) and files
(0600). `get-status` reads saved facts without initialization or capture.

## 2. Mail and exact replies

One send stages a complete stable-ID draft, then publishes independent
receiver-inbox and sender-sent Markdown copies. A minimal transaction record
recovers interrupted multi-file publication. The envelope is a one-line JSON
HTML comment followed by literal Markdown. POP claims only the caller's mail,
moves it to its final read path, and writes a claim receipt. Concurrent POP
cannot claim it twice. Output failure never makes a claimed message eligible
again; read history is directly recoverable. Read the complete archive through
EOF before interpreting or replying.

A reply addresses its actual claimed original ID. The tool supplies exact
request correlation, sender and recipient; one combined reply may fill several
claimed requests sharing one return route. Generic ACKs and unrelated
sends do not discharge obligations. Mail-derived self-state separates unread
attention, inbound replies owed and outbound replies awaited.

## 3. Routing and activation

Local mail requires an explicit Mermaid edge. Cross-session mail requires a
declared direct parent/child relationship and the exact interface roles in
`workspace_tree`; both named native runtimes are verified at send time.
Directory ancestry and same-named local roles cannot authorize or redirect
mail. Each workspace retains independent ownership, inbox/read and sent files.

After durable publication, the same CLI makes a bounded attempt through the
common [tmux input transport](notification.md). Sending never waits for POP,
response, or work completion. The existing agent console receives the hint;
no persistent Postman worker or model API adapter is introduced.

## 4. Commands and observations

[Command approval](../../help/execution.md) uses the same mail and notification
path. The synchronous wrapper consumes only its own exactly matched decision.
No mailbox lock spans approval waiting or Bash execution. Audit records never
become a resumable execution queue or reusable authority.

Send and POP always trigger bounded native snapshots across their verified
session's windows. Shared windows are excluded; unconfigured panes retain only
native identity. Immutable history plus a small latest-sample index prevents
unbounded history scans. Capture errors never undo mail. All saved logs and
observations remain until user deletion; there is no expiry or rotation.

Timestamps record successful POP, capture and observed screen differences. No
activity state machine, automatic compaction observer, persistent observer,
auto-PING, TUI, background delivery or daemon startup surface remains. The CLI
reports file-derived counts using a common node/self shape. Native text does not
establish model identity or continuous work.

Herdr support is discontinued. Historical research is not a current setup gate
or a future support commitment. Old histories are not migrated, deleted or used
as execution authority. See the [CLI specification](../../help/cli-spec.md) for
the complete current interface and keep/simplify/remove inventory.
