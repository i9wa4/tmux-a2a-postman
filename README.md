# tmux-a2a-postman

tmux agent-to-agent message delivery daemon.

## 1. Installation

```sh
go install github.com/i9wa4/tmux-a2a-postman/cmd/tmux-a2a-postman@latest
```

## 2. How it Works

tmux-a2a-postman automatically discovers and connects agents running in the same tmux session:

1. Detects panes with `A2A_NODE` environment variable set (e.g., `A2A_NODE=worker`)
2. Sends PING messages to discovered nodes to establish communication
3. Routes messages between nodes based on configured edges

**Current limitation**: Communication works within a single tmux session only. Cross-session messaging is planned for future releases.

## 3. Quick Start

Start the postman daemon:

```sh
# Interactive mode with TUI (default)
tmux-a2a-postman

# The TUI allows you to:
# - Select tmux sessions to monitor
# - Send PING to nodes (press 'p')
# - View message events in real-time
```

## 4. Directory Structure (XDG Base Directory)

postman uses XDG Base Directory Specification:

- **Config**: `$XDG_CONFIG_HOME/postman/postman.toml` (default: `~/.config/postman/`)
- **State**: `$XDG_STATE_HOME/postman/` (default: `~/.local/state/postman/`)

### 4.1. Session Directory Structure

```text
$XDG_STATE_HOME/postman/
└── session-{contextId}/
    ├── inbox/{node}/   # Incoming messages per node
    ├── post/           # Outgoing messages
    ├── draft/          # Message drafts
    ├── read/           # Processed messages
    └── dead-letter/    # Undeliverable messages
```

## 5. Environment Variables

### 5.1. A2A_NODE

**Required** for agent nodes to be discovered by postman.

Set the node name for the current agent process. postman discovers nodes by scanning tmux panes for this environment variable.

**Examples**:

```sh
# Set node name for orchestrator
export A2A_NODE=orchestrator

# Set node name for worker
export A2A_NODE=worker
```

### 5.2. Other Variables

Additional environment variables (`POSTMAN_HOME`, `A2A_CONTEXT_ID`, etc.) are available for advanced configuration. See `internal/config/postman.default.toml` for details.

## 6. Configuration

postman reads configuration from `$XDG_CONFIG_HOME/postman/postman.toml` (or use `--config` flag).

Configuration files define:

- **Routing rules** (edges): Which nodes can communicate with each other
- **Node templates**: Instructions shown to each node when they join
- **Message templates**: Format for notifications and drafts

### 6.1. Flexible Routing with Edges

Edges define bidirectional communication paths between nodes. You can build any topology by combining edge definitions.

**Example topology**:

```mermaid
graph TD
    user([User]) -.-> concierge
    concierge --- orchestrator
    orchestrator --- worker
    orchestrator --- observer-a-leader
    orchestrator --- observer-b-leader
    observer-a-leader --- observer-b-leader
```

Each edge creates bidirectional routes. For example, `"A -- B"` allows both A→B and B→A communication.

Nodes can only communicate when an edge exists between them. If only "concierge -- orchestrator" is defined, `worker` cannot send messages directly to `concierge`.

### 6.2. Complete Configuration Example

File: `$XDG_CONFIG_HOME/postman/postman.toml`

```toml
[postman]
# Routing edges (bidirectional)
edges = [
  "concierge -- orchestrator",
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
Inbox: {inbox_path}

Reply:
1. {reply_command}
2. Edit content
3. mv from draft/ to post/
"""

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
- **Template variables**: `{from_node}`, `{filename}`, `{inbox_path}`, and `{reply_command}` are automatically filled when messages arrive
- **Single file**: All configuration (`[postman]`, `[orchestrator]`, `[worker]`) in one place

### 6.3. Routing Management

**NOTE:** Editing edges via TUI will remove comments from postman.toml.
Manual editing is recommended for preserving comments.

## 7. Usage

```sh
# Start daemon
tmux-a2a-postman start --context-id <session-id> [--config path/to/config.toml]

# Create draft message
tmux-a2a-postman create-draft --to <recipient> --context-id <session-id> --from <sender>

# Show version
tmux-a2a-postman version
```
