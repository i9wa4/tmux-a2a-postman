# tmux-a2a-postman

tmux agent-to-agent message delivery daemon.

## 1. Installation

```sh
go install github.com/i9wa4/tmux-a2a-postman@latest
```

### 1.1. Version Handling

This project uses git tags as the single source of truth for versions.

**Version format depends on build context:**

| Build Type     | Version Format   | Example     |
| -------------- | ---------------- | ----------- |
| GitHub release | Semantic version | v0.2.0      |
| Local clean    | Commit hash      | git-cb6db3c |
| Local dirty    | Generic dev      | dev         |

**Technical limitation:** Nix flakes don't expose git tag information to local builds.

- Running `nix build` locally will show commit hash, even if a tag exists
- To verify release versions: `nix build github:i9wa4/tmux-a2a-postman?ref=v0.2.0`

This is a constraint of Nix's architecture, not a design choice.

**Check your version:**

```sh
# From official release (shows semantic version)
tmux-a2a-postman --version
# Output: tmux-a2a-postman v0.2.0

# From local nix build (shows commit hash)
nix build
./result/bin/tmux-a2a-postman --version
# Output: tmux-a2a-postman git-abc1234
```

## 2. How it Works

tmux-a2a-postman automatically discovers and connects agents running in the same tmux session:

1. Detects panes with a tmux pane title set (e.g., `tmux rename-pane worker`)
2. Sends PING messages to discovered nodes to establish communication
3. Routes messages between nodes based on configured edges

**Current limitation**: Communication works within a single tmux session only. Cross-session messaging is planned for future releases.

**Note**: The TUI displays all tmux sessions (including those without A2A nodes) for monitoring purposes, even though message routing is limited to the active session.

## 3. Quick Start

Start the postman daemon:

```sh
# Interactive mode with TUI (default)
tmux-a2a-postman

# The TUI allows you to:
# - View all tmux sessions (including those without A2A nodes)
# - Toggle sessions enabled/disabled for automatic PING
# - Send PING to nodes (press 'p')
# - View message events in real-time
```

## 4. Session Management

Sessions can be toggled between enabled and disabled states in the TUI. This controls automatic PING behavior:

### 4.1. Session States

- **Enabled**: Session receives automatic PING when new nodes are detected
- **Disabled**: Session does NOT receive automatic PING (default for new sessions)

### 4.2. Automatic Session Enable Behavior

Two config fields control how sessions and agents are auto-enabled:

| Field                      | Default | Behavior                                                         |
| -------------------------- | ------- | ---------------------------------------------------------------- |
| `auto_enable_new_sessions` | `false` | Whether newly discovered sessions are automatically enabled      |
| `auto_enable_new_agents`   | `true`  | Whether new agents in an already-enabled session are auto-pinged |

By default, new sessions require explicit enabling in the TUI before they receive automatic PING. This opt-in design prevents unintentional PING delivery to newly joined sessions.

**Bool merge limitation**: Setting `auto_enable_new_sessions = false` in a project-local config file will NOT override an XDG-level `true`. Bool fields only propagate `true` values — the Go zero-value (`false`) is indistinguishable from "field not set". Use the XDG config to set these fields definitively.

### 4.3. Automatic PING Paths

Automatic PING is sent to new nodes at three detection points:

1. **New node detection** (message delivery): When a node is discovered during message routing
2. **Periodic discovery scan**: When the daemon detects a new node during its scan interval
3. **Pane restart detection**: When a pane restart is detected for an existing node

All three paths respect the session enabled state. Disabled sessions will not receive automatic PING at any of these paths.

### 4.4. Manual PING

Manual PING (via 'p' key in TUI) always works regardless of session state. This allows you to manually initialize nodes in disabled sessions if needed.

Manual PING uses fresh node discovery at invocation time and falls back to the daemon's cached node snapshot if discovery fails.

## 5. Directory Structure (XDG Base Directory)

postman uses XDG Base Directory Specification:

- **Config**: `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml` (default: `~/.config/tmux-a2a-postman/`)
- **State**: `$XDG_STATE_HOME/tmux-a2a-postman/` (default: `~/.local/state/tmux-a2a-postman/`)

### 5.1. Session Directory Structure

```text
$XDG_STATE_HOME/tmux-a2a-postman/
└── session-{contextId}/
    ├── inbox/{node}/   # Incoming messages per node
    ├── post/           # Outgoing messages
    ├── draft/          # Message drafts
    ├── read/           # Processed messages
    └── dead-letter/    # Undeliverable messages
```

## 6. Environment Variables

### 6.1. Pane Title (Node Identity)

**Required** for agent nodes to be discovered by postman.

Set the tmux pane title to identify a pane as an agent node. postman discovers nodes by reading the pane title directly.

**Examples**:

```sh
# Set pane title for orchestrator
tmux rename-pane orchestrator

# Set pane title for worker
tmux rename-pane worker
```

**Watchdog mode**: a pane with title `watchdog` runs the watchdog daemon instead of the regular daemon.

### 6.2. Other Variables

Additional environment variables (`POSTMAN_HOME`, `A2A_CONTEXT_ID`, etc.) are available for advanced configuration. See `internal/config/postman.default.toml` for details.

## 7. Configuration

postman reads configuration from `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml` (or use `--config` flag).

Configuration files define:

- **Routing rules** (edges): Which nodes can communicate with each other
- **Node templates**: Instructions shown to each node when they join
- **Message templates**: Format for notifications and drafts

### 7.1. Flexible Routing with Edges

Edges define bidirectional communication paths between nodes. You can build any topology by combining edge definitions.

**Example topology**:

```mermaid
graph TD
    user([User]) -.- messenger
    messenger --- orchestrator
    orchestrator --- worker
    orchestrator --- observer-a-leader
    orchestrator --- observer-b-leader
    observer-a-leader --- observer-b-leader
```

Each edge creates bidirectional routes. For example, `"A -- B"` allows both A->B and B->A communication.

Nodes can only communicate when an edge exists between them. If only "messenger -- orchestrator" is defined, `worker` cannot send messages directly to `messenger`.

### 7.2. Complete Configuration Example

File: `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml`

```toml
[postman]
# Routing edges (bidirectional)
edges = [
  "messenger -- orchestrator",
  "orchestrator -- worker",
  "orchestrator -- observer-a-leader",
  "orchestrator -- observer-b-leader",
  "observer-a-leader -- observer-b-leader",
]

# Reply command template (expanded with {context_id})
reply_command = "tmux-a2a-postman create-draft --context-id {context_id} --to <recipient>"

# Notification template (shown when messages arrive)
notification_template = """
Message from {from_node}

File: {filename}
Inbox: {session_dir}/inbox/{node}/

Reply:
1. {reply_command}
2. Edit content
3. mv from draft/ to post/
"""

# Auto-enable behavior (default: opt-in)
auto_enable_new_sessions = false  # Set true to auto-enable all new sessions
auto_enable_new_agents = true     # Auto-ping new agents in already-enabled sessions

# Reminder feature: send reminder message after N messages delivered to a node
# Set to 0 to disable (default: 0)
reminder_interval_messages = 0
reminder_message = ""

# Node configurations
[orchestrator]
role = "coordination, delegation"
template = """
# ORCHESTRATOR (READONLY)

- Delegate tasks to workers
- Never edit files directly
"""

[worker]
role = "implementation"
template = """
# WORKER (WRITABLE)

- Execute assigned tasks
- Report issues to orchestrator
"""
```

**Key features**:

- **`{reply_command}`**: Automatically expands to include the current context ID, so recipients know exactly how to reply
- **Template variables**: `{from_node}`, `{filename}`, `{session_dir}`, `{node}`, and `{reply_command}` are automatically filled when messages arrive
  - `{session_dir}/inbox/{node}/`: Incoming messages directory
  - `{session_dir}/read/`: Processed messages directory
  - `{session_dir}/draft/`: Message drafts directory
  - `{session_dir}/post/`: Outgoing messages directory
- **Configuration files**: Supports both single file (`postman.toml`) and split files (`postman.toml` + `nodes/*.toml`)

### 7.3. Reminder Feature

postman can send a reminder message to a node after it has received a configured number of messages.

```toml
[postman]
reminder_interval_messages = 5  # Send reminder every 5 messages delivered
reminder_message = "REMINDER: You have {count} pending messages for {node}"
```

Per-node override is also supported:

```toml
[worker]
reminder_interval_messages = 3
reminder_message = "Worker: {count} messages pending"
```

**Template variables**: `{node}` (node name), `{count}` (message count since last reminder).

The counter increments on each message delivered to the node and resets after the reminder fires. The reminder is not based on whether the node has replied — it tracks delivery count only.

When the reminder fires, postman sends the expanded message to the node's pane using the standard `SendToPane` path (same as regular message delivery).

Set `reminder_interval_messages = 0` (default) to disable the reminder feature.

### 7.4. Routing Management

**NOTE:** Editing edges via TUI will remove comments from postman.toml.
Manual editing is recommended for preserving comments.

## 8. Usage

```sh
# Start daemon
tmux-a2a-postman start --context-id <session-id> [--config path/to/config.toml]

# Create draft message
tmux-a2a-postman create-draft --to <recipient> --context-id <session-id> --from <sender>

# Show version
tmux-a2a-postman --version
```

### 8.1. Session Status at a Glance

Show all tmux sessions' pane status in a single line:

```sh
tmux-a2a-postman get-session-status-oneline
```

**Requirements:**

- Daemon must be running (uses daemon's idle tracking state)
- Pane capture must be enabled in config (default: enabled)

**Output format:** `[S0:window_panes:window_panes:...] [S1:window_panes:...]`

Example:

```text
[S0:🟢🟡🔴:🟢🔴] [S1:🔴🔴🔴🔴:🔴🔴]
```

**Status indicators:**

- 🟢 Active: last content change within `node_active_seconds` (default 300s / 5 min)
- 🟡 Idle: last content change between `node_active_seconds` and `node_idle_seconds` ago (defaults: 5-15 min)
- 🔴 Stale: last content change more than `node_idle_seconds` ago (default 900s / 15 min)
- Sessions ordered by name
- Windows separated by `:` within each session
- Session IDs (S0, S1, ...) correspond to the session index order

**Note:** Status thresholds are configurable via `node_active_seconds` (default: 300)
and `node_idle_seconds` (default: 900) in `postman.toml`.
Active transitions to Idle when elapsed time exceeds `node_active_seconds`;
Idle transitions to Stale when elapsed time exceeds `node_idle_seconds`.
