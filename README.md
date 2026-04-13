# tmux-a2a-postman

tmux agent-to-agent message delivery daemon.

## 1. Prerequisites

- tmux >= 3.0

## 2. Installation

```sh
go install github.com/i9wa4/tmux-a2a-postman@latest
```

Or with Nix:

```sh
nix run github:i9wa4/tmux-a2a-postman
```

## 3. Concept

tmux-a2a-postman is a **daemon** that discovers AI agents running in tmux
panes and routes messages between them via filesystem-based inboxes.

```mermaid
graph TD
    subgraph "tmux session: my-project"
        messenger["messenger\n(Claude Code)"]
        orchestrator["orchestrator\n(Codex CLI)"]
        worker["worker\n(Claude Code)"]
        worker-alt["worker-alt\n(Codex CLI)"]
    end
    daemon["postman daemon (TUI)\n- discovers panes by title\n- routes messages via edges\n- delivers to inbox/{node}/"]
    messenger --- daemon
    orchestrator --- daemon
    worker --- daemon
    worker-alt --- daemon
```

### 3.1. Message Flow

Example: orchestrator delegates a task to worker.

1. orchestrator sends:
   `tmux-a2a-postman send --to worker --body "implement X"`
2. Daemon routes the message (edge rules enforced)
3. worker is notified in their pane
4. worker reads: `tmux-a2a-postman pop`
5. worker replies:
   `tmux-a2a-postman send --to orchestrator --body "DONE: ..."`

For thread-bound review or approval traffic, add a durable `thread_id` inside
the message `params:` block. Non-thread traffic can omit it. When present, the
daemon preserves that `thread_id` in journal-backed mailbox shadow and
waiting-file events, and approval projection only materializes from events that
carry the durable thread identity.

### 3.2. ui_node

`ui_node` designates the human-facing interface. The embedded defaults route
daemon alerts to `messenger`; override `ui_node` if you want a different
recipient, or set it to an empty string to turn the alert path off.

```toml
[postman]
ui_node = "messenger"
```

### 3.3. Node Discovery

Agents are discovered by their **tmux pane title**. Set titles to match node
names defined in the configuration:

```sh
tmux select-pane -T orchestrator
tmux select-pane -T worker
```

## 4. Configuration

Configuration uses two file formats: TOML for structural settings and Markdown
for templates. Both live in `$XDG_CONFIG_HOME/tmux-a2a-postman/`.

### 4.1. Edges and Node Topology

Define which nodes can communicate using edges:

```mermaid
graph LR
    messenger --- orchestrator
    orchestrator --- worker
    orchestrator --- worker-alt
    orchestrator --- critic
    guardian --- critic
```

In `postman.md`:

````markdown
## `edges`

```mermaid
graph LR
    messenger --- orchestrator
    orchestrator --- worker
    orchestrator --- worker-alt
    orchestrator --- critic
    guardian --- critic
```
````

Or in `postman.toml`:

```toml
[postman]
edges = [
  "messenger -- orchestrator",
  "orchestrator -- worker",
  "orchestrator -- worker-alt",
  "orchestrator -- critic",
  "guardian -- critic",
]
```

Messages sent to nodes without a valid edge are moved to `dead-letter/`.

### 4.2. Node Role Templates

Define each node's role and template in `postman.md`:

````markdown
## `worker`

### `role`

Primary task executor.

### Workflow

Execute tasks from orchestrator. Report DONE or BLOCKED.
````

Or as separate files in `nodes/worker.md`.

Markdown frontmatter uses a small explicit subset. It is not real YAML.

Supported syntax:

- A leading `---` frontmatter block
- One single-line `key: value` pair per non-empty line
- Leading or trailing whitespace around a top-level `key: value` entry is
  trimmed
- Values may contain extra `:` characters because parsing splits on the first
  `:`
- Quotes are treated as literal characters

Unsupported syntax:

- List items such as `- worker`
- Nested mappings or indented continuation lines
- Multi-line values
- Comment lines inside frontmatter
- Unclosed frontmatter blocks

Unsupported frontmatter fails at load time with a precise error instead of being
silently ignored.

### 4.3. File Layout

```text
$XDG_CONFIG_HOME/tmux-a2a-postman/
  postman.toml          # structural config (timing, thresholds)
  postman.md            # templates, edges (Mermaid), node definitions
  nodes/
    worker.md           # per-node template (optional, overrides postman.md)
    orchestrator.md
```

### 4.4. Project-Local Override

Place config files in `.tmux-a2a-postman/` inside your project directory
to override XDG config:

```text
your-project/
  .tmux-a2a-postman/
    postman.toml        # structural overrides
    postman.md          # template overrides
    nodes/
      worker.md
```

**Nix/home-manager users:** if your XDG config is read-only Nix store
symlinks, use project-local overrides.

### 4.5. Unified state + notification model

get-health, get-health-oneline, and the default TUI are three views over the
same canonical contract. The per-node visible states are `ready`,
`pending`, `user_input`, `composing`, `spinning`, and `stalled`. Session-level
`unavailable` is a fallback that means the current daemon is not authoritative
for canonical health; it is not a per-node state.

Quick reading guide:

| If you see | It means | Tune / read next |
| ---------- | -------- | ---------------- |
| `pending`, `user_input`, `composing`, `spinning`, or `stalled` in `get-health`, `get-health-oneline`, or the TUI | Canonical visible state for a node right now | `docs/design/node-state-machine.md` |
| A pane hint telling a node to run `tmux-a2a-postman pop` | Delivery reached that node's inbox; this is a pane notification, not a new state | `docs/design/notification.md` |
| A daemon-generated message routed to `ui_node` | Policy alert such as unread summary, inactivity, unreplied message, expected-reply overdue, or a stalled reply-tracked wait | `docs/guides/alert-config.md` and `docs/design/notification.md` |
| A dropped-ball event in the TUI or tmux status bar | Coordination warning based on `LastReceived > LastSent`; by default it is not an inbox alert | `docs/guides/alert-config.md` |
| PING or heartbeat mail | Control-plane traffic that is still operator-visible in the current tree | `docs/design/notification.md` |

Public knobs for this model live in `postman.toml`:

- `ui_node`
- `reminder_interval_messages`
- `inbox_unread_threshold`
- `journal_health_cutover_enabled`
- `journal_compatibility_cutover_enabled`
- `retention_period_days`
- `message_footer`
- `[node].idle_timeout_seconds`
- `[node].dropped_ball_timeout_seconds`
- `node_spinning_seconds`
- `[heartbeat].enabled`

The journal cutover flags form three valid modes. `legacy` is the default with
both flags off. `health-first` enables journal-backed canonical health while
`send` and `pop` still write mailbox state directly. `compatibility-first`
enables both journal-backed health and compatibility-submit mailbox delivery.
`journal_compatibility_cutover_enabled = true` without
`journal_health_cutover_enabled = true` is invalid, and `start` rejects that
config.

For stored messages written by `send`, reply guidance comes from
`message_footer` in `internal/config/postman.default.toml`. TOML config and XDG
`postman.md` can replace that footer; project-local `postman.md` appends its
`message_footer` to the effective base footer. Daemon alerts and heartbeat mail
use `daemon_message_template`, and dead-letter notifications write their own
re-send instructions. `pop` prints the stored message as written and does not
add a second hard-coded reply footer.

Advanced dampening and rendering-shaping fields remain documented in
`docs/design/notification.md` and `docs/guides/alert-config.md`.
The embedded defaults in `internal/config/postman.default.toml` currently route
alerts to `messenger`.

### 4.6. Priority Order (highest to lowest)

1. Project-local `postman.md`
2. Project-local `nodes/*.md`
3. Project-local `nodes/*.toml`
4. Project-local `postman.toml`
5. XDG `postman.md`
6. XDG `nodes/*.md`
7. XDG `nodes/*.toml`
8. XDG `postman.toml`
9. Embedded defaults (`internal/config/postman.default.toml`)

All default values are defined in `postman.default.toml` (SSOT).

## 5. Running the Daemon

```sh
# Start daemon (interactive single-column TUI)
tmux-a2a-postman start

# Headless mode (no TUI surface; for CI or automated environments)
tmux-a2a-postman start --no-tui

# Stop daemon
tmux-a2a-postman stop
```

The default operator loop is `send`, `pop`, `get-health`, and
`get-health-oneline`. Lifecycle and recovery commands such as `start`, `stop`,
and `get-context-id` remain available, but they are no longer the main
beginner/operator surface. Use explicit subcommands; bare
`tmux-a2a-postman` prints usage and does not start the daemon.

## 6. Directory Structure

Base directory resolution, in priority order

1. `$POSTMAN_HOME`
2. `base_dir` in config
3. `$XDG_STATE_HOME/tmux-a2a-postman`

Falls back to `~/.local/state/tmux-a2a-postman` when `XDG_STATE_HOME` is unset

```text
{baseDir}/
  lock/                 # preserved: session locks for live-daemon ownership
  {contextId}/
    postman.log         # disposable: startup retention may prune inactive contexts
    pane-activity.json  # disposable: startup retention may prune inactive contexts
    phony/              # preserved: binding-backed inbox and dead-letter state
    supervisor-memory/  # preserved: supervisor memory store and dead-letters
    {sessionName}/
      postman.pid       # live-daemon marker for this tmux session
      draft/            # internal: draft staging area (use send instead)
      post/             # internal: outbox queue managed by postman daemon
      inbox/{node}/     # daemon delivers messages here
      read/             # agent moves messages here after reading
      dead-letter/      # unroutable messages land here
      waiting/          # per-node waiting state files
```

Runtime lifecycle classes

| Path | Lifecycle | Startup retention behavior |
| ---- | --------- | -------------------------- |
| `{baseDir}/lock/` | Active coordination state | Always preserved |
| `{baseDir}/{contextId}/{sessionName}/` | Session runtime state | Eligible only when the context has no live `postman.pid` anywhere under it |
| `{baseDir}/{contextId}/postman.log` | Context-local log | Eligible only when the context is inactive |
| `{baseDir}/{contextId}/pane-activity.json` | Context-local pane snapshot cache | Eligible only when the context is inactive |
| `{baseDir}/{contextId}/phony/` | Durable phony-node delivery state | Always preserved |
| `{baseDir}/{contextId}/supervisor-memory/` | Durable supervisor memory state | Always preserved |

`retention_period_days` controls that startup cleanup window. The embedded
default is `90`. Set it to `0` to disable the broader inactive-context sweep.
Cleanup keeps base-dir and XDG resolution unchanged, skips any context with a
live daemon, and preserves unknown entries by default instead of guessing.

## 7. Deployment Model

The default operator model is one daemon per observed tmux session. Start the
daemon for the session you are operating and treat that daemon as the canonical
health and alert authority for the session.

Only one live daemon may own a given tmux session at a time. Running additional
daemons elsewhere is an advanced/internal topology detail, not part of the
normal operator workflow or the reduced beginner surface.

See `docs/design/daemon-session-model.md` for the full daemon/session model.

## 8. CLI Reference

The README teaches the beginner/operator loop. Use
[docs/commands.md](docs/commands.md) as the exact CLI reference for flag
tables, `--json` output shapes, `--params` usage, and canonical command names.

Default operator surface:

```text
tmux-a2a-postman send --to worker --body "hello"
tmux-a2a-postman pop
tmux-a2a-postman get-health
tmux-a2a-postman get-health-oneline --json
```

Lifecycle and recovery:

```text
tmux-a2a-postman start
tmux-a2a-postman stop
tmux-a2a-postman get-context-id
tmux-a2a-postman help [TOPIC]       # built-in help (topics: messaging, directories, config, commands)
tmux-a2a-postman schema [COMMAND]   # JSON Schema for the public config surface or a command's --params scope
```

Diagnostic and cutover tooling:

```text
tmux-a2a-postman timeline --limit 20        # recent redacted journal events for the live session
tmux-a2a-postman replay --surface mailbox   # read-only projected mailbox paths from the journal
```

Migration from older names:

| Older name                      | Current path              | Note |
| ------------------------------- | ------------------------- | ---- |
| `send-message`                  | `send`                    | Use `send` in the default operator loop |
| `get-session-health`            | `get-health`              | `get-health` is the canonical JSON payload |
| `get-session-status-oneline`    | `get-health-oneline`      | `get-health-oneline` is the compact all-session formatter over canonical health |

## 9. Skills

The `skills/` directory contains reusable agent skill files for AI coding
assistants (Claude Code, Codex CLI, etc.). Each skill lives at
`skills/{skill-name}/SKILL.md`.

- send: Sends messages to another node using tmux-a2a-postman send.
- a2a-role-auditor: Audits node role templates to diagnose and fix
  node-to-node interaction breakdowns.
