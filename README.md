# tmux-a2a-postman

tmux agent-to-agent message delivery daemon.

## 1. Installation

```sh
go install github.com/i9wa4/tmux-a2a-postman@latest
```

Version format depends on build context:

| Build Type     | Version Format   | Example     |
| -------------- | ---------------- | ----------- |
| GitHub release | Semantic version | v0.2.0      |
| Local clean    | Commit hash      | git-cb6db3c |
| Local dirty    | Generic dev      | dev         |

## 2. How it Works

Discovers agents in the same tmux session by reading pane titles, sends PING
messages to establish communication, and routes messages between nodes based on
configured edges.

Communication works within a single tmux session by default. Cross-session
routing is available when `diplomat_node` is configured (#164).

## 3. Quick Start

Set pane titles to identify nodes:

```sh
tmux rename-pane orchestrator
tmux rename-pane worker
```

Start the daemon (interactive TUI):

```sh
tmux-a2a-postman
```

## 4. Directory Structure (XDG Base Directory)

- **Config**: `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml`
- **State**: `$XDG_STATE_HOME/tmux-a2a-postman/`

```text
$XDG_STATE_HOME/tmux-a2a-postman/
├── diplomat/
│   └── {targetContextId}/
│       └── post/       # Cross-session drops (diplomat, #164)
└── session-{contextId}/
    └── {sessionName}/
        ├── inbox/{node}/   # Incoming messages per node
        ├── post/           # Outgoing messages
        ├── draft/          # Message drafts
        ├── read/           # Processed messages
        └── dead-letter/    # Undeliverable messages
```

## 5. Session Management

Sessions are toggled enabled/disabled in the TUI, controlling automatic PING
delivery. Two config fields govern auto-enable behavior:
`auto_enable_new_sessions` (default `false`) controls whether newly discovered
sessions are automatically enabled; `auto_enable_new_agents` (default `true`)
controls whether new agents in an already-enabled session are auto-pinged.
Manual PING (press `p` in TUI) always works regardless of session state.

| Field                      | Default | Behavior                                               |
| -------------------------- | ------- | ------------------------------------------------------ |
| `auto_enable_new_sessions` | `false` | Auto-enable newly discovered sessions                  |
| `auto_enable_new_agents`   | `true`  | Auto-ping new agents in already-enabled sessions       |

**Bool merge limitation**: Setting `auto_enable_new_sessions = false` in a
project-local config file will NOT override an XDG-level `true`. Bool fields
only propagate `true` values — the Go zero-value (`false`) is indistinguishable
from "field not set". Use the XDG config to set these fields definitively.

### 5.1. Session ON Constraint

Only one tmux-a2a-postman daemon may have a given tmux session turned ON at
a time. If daemon A has session `foo` ON, daemon B will be blocked from also
turning `foo` ON.

**Multiple daemons may run** on the same tmux server — this is by design.
Each daemon manages its own context and sessions. The constraint applies to
the session-ON state only: two daemons cannot both be routing messages for
the same session.

If you see "session already ON" at startup, either:

- Turn OFF the session in the other daemon's TUI before starting, or
- Stop the other daemon with `tmux-a2a-postman stop`.

If the blocking daemon crashed and left a stale tmux option, clear it manually:

```sh
tmux set-option -gu @a2a_session_on_<sessionName>
```

Replace `<sessionName>` with your actual tmux session name (e.g.,
`tmux-a2a-postman`).

## 6. Environment Variables

### 6.1. Pane Title (Node Identity)

**Required** for agent nodes to be discovered by postman. Set the tmux pane
title to identify a pane as an agent node:

```sh
tmux rename-pane orchestrator
tmux rename-pane worker
```

### 6.2. Other Variables

See `internal/config/postman.default.toml` for advanced variables
(`POSTMAN_HOME`, etc.).

## 7. Configuration

`$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml`:

```toml
[postman]
edges = [
  "messenger -- orchestrator",
  "orchestrator -- worker",
]
reply_command = "tmux-a2a-postman send-message --to <recipient> --body \"<your message>\""

[orchestrator]
role = "coordination"
template = "..."

[worker]
role = "implementation"
template = "..."
```

Key config options:

| Key                          | Default | Description                                    |
| ---------------------------- | ------- | ---------------------------------------------- |
| `edges`                      | `[]`    | Bidirectional routing edges (`"A -- B"`)       |
| `message_template`           | (yaml)  | Envelope written to inbox for daemon-sent messages (PING) |
| `notification_template`      | (path)  | Pane hint sent when new message arrives (default: file path only) |
| `ping_mode`                  | `"all"` | `"all"`, `"ui_node_only"`, `"disabled"`         |
| `auto_enable_new_sessions`   | `false` | See Session Management                         |
| `auto_enable_new_agents`     | `true`  | See Session Management                         |
| `node_active_seconds`        | `300`   | Active threshold (seconds)                     |
| `node_idle_seconds`          | `900`   | Idle threshold (seconds)                       |
| `reminder_interval_messages` | `0`     | Reminder after N deliveries; `0` = disabled    |

See `internal/config/postman.default.toml` for all available options.

**NOTE:** Editing edges via TUI removes comments from `postman.toml`; manual
editing is recommended for preserving comments.

### 7.1. Project-Local Configuration Override

Place config files in `.tmux-a2a-postman/` inside your project directory
to override XDG config without modifying `~/.config/`:

```text
your-project/
└── .tmux-a2a-postman/
    ├── postman.toml        # required sentinel (can be empty); also overrides [postman] settings
    └── nodes/
        ├── worker.toml     # overrides $XDG_CONFIG_HOME/.../nodes/worker.toml
        └── orchestrator.toml
```

**Priority order (highest to lowest):**

1. Project-local `nodes/*.toml`
2. Project-local `postman.toml` node sections
3. XDG `nodes/*.toml`
4. XDG `postman.toml`
5. Embedded defaults

**Setup:**

```sh
mkdir -p .tmux-a2a-postman/nodes
# postman.toml sentinel is required for the overlay to activate:
touch .tmux-a2a-postman/postman.toml
# Copy and edit an existing node file:
cp ~/.config/tmux-a2a-postman/nodes/worker.toml .tmux-a2a-postman/nodes/
# Verify the override is active:
tmux-a2a-postman get-nodes-dir
```

NOTE: `.tmux-a2a-postman/postman.toml` must exist (even if empty) as a
sentinel for the project-local overlay to activate. Without it, nodes in
`.tmux-a2a-postman/nodes/` are silently ignored.

**Nix/home-manager users:** if your XDG nodes are read-only Nix store
symlinks, use project-local `nodes/` as the SSOT — editable in-place, version
controlled with git, no `home-manager switch` required.

## 8. Usage

```sh
# Start daemon (interactive TUI)
tmux-a2a-postman start [--context-id <id>] [--config path/to/config.toml]

# Send a message (atomic, one-step)
tmux-a2a-postman send-message --to <recipient> --body "text"

# Read and archive the oldest unread message
tmux-a2a-postman next

# Count unread inbox messages
tmux-a2a-postman count

# Print current context ID (useful for AI agents)
tmux-a2a-postman get-context-id

# Advanced: create draft, edit, then send (for long or cross-context messages)
tmux-a2a-postman create-draft --to <recipient>
tmux-a2a-postman send <filename>

# Archive inbox message (mark as read)
tmux-a2a-postman archive <filename>

# Show pane status summary (plain emoji when piped, ANSI colored dots in a terminal)
tmux-a2a-postman get-session-status-oneline

# Show version
tmux-a2a-postman --version
```

### 8.1. Headless / CI Usage

Run the daemon without the interactive TUI (non-interactive mode):

```sh
tmux-a2a-postman --no-tui [--context-id <id>]
```

In headless mode:

- Message routing and delivery work normally
- No interactive dashboard is displayed
- Logs are written to stderr (redirect with `--log-file`)
- Useful for CI pipelines and automated testing environments

### 8.2. Recommended Shell Alias

```sh
alias a2a='tmux-a2a-postman send-message'
# Usage: a2a --to <recipient> --body "text"
```

### 8.3. tmux status-right Integration

To show agent pane status in the tmux status bar, add to your `~/.tmux.conf`:

```tmux
set -g status-right '#(tmux-a2a-postman get-session-status-oneline)'
```

Output is plain emoji (`🟢🔵🟡🔴`) when called from `#()`, and ANSI colored
dots (`●`) when run directly in a terminal.

## 9. Deployment Topology

**Constraint**: 1 tmux session = 1 postman daemon. Multiple daemons may run on
the same machine only when they are in different tmux sessions.

Three supported configurations:

| Topology                    | tmux servers | Daemons | Machines | Diplomat required |
| --------------------------- | ------------ | ------- | -------- | ----------------- |
| Single daemon               | 1            | 1       | 1        | No                |
| Multi-daemon, same machine  | 1            | N       | 1        | No                |
| Multi-daemon, cross-machine | N            | N       | N        | Yes               |

### 9.1. Single Daemon (Primary Model)

One tmux server, one daemon, N nodes — all agents on one machine.

```text
tmux server
└── session (1 daemon)
    ├── pane: orchestrator
    ├── pane: worker
    └── pane: messenger
```

### 9.2. Multi-Daemon, Same Machine

One tmux server, multiple daemons with distinct context IDs — useful for
isolated project contexts running in parallel.

```text
tmux server
├── session-A (daemon A)
│   ├── pane: orchestrator
│   └── pane: worker
└── session-B (daemon B)
    ├── pane: orchestrator
    └── pane: worker
```

Each daemon maintains its own `{base_dir}/{contextId}/` state directory.
Sessions are isolated from each other by default.

### 9.3. Multi-Daemon, Cross-Machine

Multiple machines each running a daemon, sharing a common `base_dir` via a
shared filesystem (NFS, SSHFS, Syncthing, etc.).

```text
machine-A                    machine-B
└── session (daemon A)  ←→  └── session (daemon B)
    shared base_dir ─────────── shared base_dir
```

**Required for the diplomat feature** (`diplomat_node` config): cross-context
messaging depends on all participating daemons writing to the same `base_dir`
path on a common filesystem. Each machine runs its own daemon; the shared
filesystem is the only coupling.

## 10. Skills

The `skills/` directory contains reusable agent skill files for use with AI
coding assistants (Claude Code, Codex CLI, etc.). Each skill lives at
`skills/{skill-name}/SKILL.md` and is invoked via the assistant's skill
mechanism (e.g., `/a2a-role-auditor` in Claude Code).

### 10.1. a2a-role-auditor

Path: `skills/a2a-role-auditor/SKILL.md`

Audits `nodes/*.toml` role templates to diagnose and fix node-to-node
interaction breakdowns.

Use when:

- A node behaves unexpectedly (routes wrongly, ignores messages, approves
  nothing)
- Nodes cannot see each other in `talks_to_line` (after ruling out session/PING
  issues)
- Adding a new node and need to verify its template is complete and consistent
- Reviewing or improving role definitions for any node

Do NOT use for daemon-level failures (dead-letter from routing/edge
misconfiguration); run triage first to determine whether the issue is
template-level.
