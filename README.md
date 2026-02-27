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

Discovers agents in the same tmux session by reading pane titles, sends PING messages to
establish communication, and routes messages between nodes based on configured edges.

Communication works within a single tmux session only.

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
└── session-{contextId}/
    ├── inbox/{node}/   # Incoming messages per node
    ├── post/           # Outgoing messages
    ├── draft/          # Message drafts
    ├── read/           # Processed messages
    └── dead-letter/    # Undeliverable messages
```

## 5. Session Management

Sessions are toggled enabled/disabled in the TUI, controlling automatic PING delivery. Two
config fields govern auto-enable behavior: `auto_enable_new_sessions` (default `false`)
controls whether newly discovered sessions are automatically enabled; `auto_enable_new_agents`
(default `true`) controls whether new agents in an already-enabled session are auto-pinged.
Manual PING (press `p` in TUI) always works regardless of session state.

| Field                      | Default | Behavior                                               |
| -------------------------- | ------- | ------------------------------------------------------ |
| `auto_enable_new_sessions` | `false` | Auto-enable newly discovered sessions                  |
| `auto_enable_new_agents`   | `true`  | Auto-ping new agents in already-enabled sessions       |

**Bool merge limitation**: Setting `auto_enable_new_sessions = false` in a project-local
config file will NOT override an XDG-level `true`. Bool fields only propagate `true` values —
the Go zero-value (`false`) is indistinguishable from "field not set". Use the XDG config to
set these fields definitively.

## 6. Environment Variables

### 6.1. Pane Title (Node Identity)

**Required** for agent nodes to be discovered by postman. Set the tmux pane title to identify
a pane as an agent node:

```sh
tmux rename-pane orchestrator
tmux rename-pane worker
```

A pane titled `watchdog` runs the watchdog daemon (required for session idle alerts).

### 6.2. Other Variables

See `internal/config/postman.default.toml` for advanced variables (`POSTMAN_HOME`, etc.).

## 7. Configuration

`$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml`:

```toml
[postman]
edges = [
  "messenger -- orchestrator",
  "orchestrator -- worker",
]
reply_command = "tmux-a2a-postman create-draft --context-id {context_id} --to <recipient>"
notification_template = """
Message from {from_node}

File: {filename}
Inbox: {session_dir}/inbox/{node}/

Reply:
1. {reply_command}
2. Edit content
3. mv from draft/ to post/
"""

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

**NOTE:** Editing edges via TUI removes comments from `postman.toml`; manual editing is
recommended for preserving comments.

## 8. Usage

```sh
# Start daemon (interactive TUI)
tmux-a2a-postman

# Start daemon with context ID
tmux-a2a-postman start --context-id <session-id> [--config path/to/config.toml]

# Create draft message
tmux-a2a-postman create-draft --to <recipient> --context-id <session-id>

# Show pane status summary
tmux-a2a-postman get-session-status-oneline

# Show version
tmux-a2a-postman --version
```
