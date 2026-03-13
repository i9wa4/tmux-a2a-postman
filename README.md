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

Communication works within a single tmux session by default. Cross-session routing is
available when `diplomat_node` is configured (#164).

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
3. tmux-a2a-postman send <file>
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

# Start daemon (context ID auto-generated if omitted)
tmux-a2a-postman start [--context-id <id>] [--config path/to/config.toml]

# Print current context ID (useful for AI agents)
tmux-a2a-postman get-context-id

# Create draft message (context ID auto-detected from tmux session)
tmux-a2a-postman create-draft --to <recipient>

# Send draft (move from draft/ to post/)
tmux-a2a-postman send <filename>

# Archive inbox message (mark as read)
tmux-a2a-postman archive <filename>

# Show pane status summary
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
alias a2a='tmux-a2a-postman create-draft'
# Usage: a2a --to <recipient>
```

## 9. Skills

The `skills/` directory contains reusable agent skill files for use with AI coding assistants
(Claude Code, Codex CLI, etc.). Each skill lives at `skills/{skill-name}/SKILL.md` and is
invoked via the assistant's skill mechanism (e.g., `/a2a-role-auditor` in Claude Code).

### 9.1. a2a-role-auditor

Path: `skills/a2a-role-auditor/SKILL.md`

Audits `nodes/*.toml` role templates to diagnose and fix node-to-node interaction breakdowns.

Use when:

- A node behaves unexpectedly (routes wrongly, ignores messages, approves nothing)
- Nodes cannot see each other in `talks_to_line` (after ruling out session/PING issues)
- Adding a new node and need to verify its template is complete and consistent
- Reviewing or improving role definitions for any node

Do NOT use for daemon-level failures (dead-letter from routing/edge misconfiguration); run
triage first to determine whether the issue is template-level.
