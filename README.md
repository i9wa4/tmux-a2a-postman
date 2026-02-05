# tmux-a2a-postman

tmux agent-to-agent message delivery daemon.

## Directory Structure (XDG Base Directory)

postman uses XDG Base Directory Specification:

- **Config**: `$XDG_CONFIG_HOME/postman/config.toml` (default: `~/.config/postman/`)
- **State**: `$XDG_STATE_HOME/postman/` (default: `~/.local/state/postman/`)

### Session Directory Structure

```
~/.local/state/postman/
└── session-{contextId}/
    ├── inbox/{node}/   # Incoming messages per node
    ├── post/           # Outgoing messages
    ├── draft/          # Message drafts
    ├── read/           # Processed messages
    └── dead-letter/    # Undeliverable messages
```

### Migration from .postman/

If migrating from older versions that used `.postman/`:

1. Move data: `mv .postman/* ~/.local/state/postman/`
2. Or set environment: `export POSTMAN_HOME=.postman`
3. Or config file: `base_dir = ".postman"`

Priority: `POSTMAN_HOME` > `config base_dir` > `XDG_STATE_HOME/postman/`

## Environment Variables

postman supports the following environment variables:

### POSTMAN_HOME

Override the base directory for session data (inbox/, post/, draft/, read/, dead-letter/).

**Priority**: `POSTMAN_HOME` > `config base_dir` > `XDG_STATE_HOME/postman/`

**Use cases**:
- **Testing**: Use local `.postman/` directory instead of `~/.local/state/postman/`
- **Python compatibility**: Match directory structure with Python postman (`.postman/session-ID/`)
- **Multi-session development**: Keep sessions isolated per project

**Examples**:
```sh
# Use local .postman/ directory
export POSTMAN_HOME=.postman
postman start --context-id test-session

# Watchdog with local directory
A2A_NODE=watchdog POSTMAN_HOME=.postman postman start --context-id test-session
```

### A2A_CONTEXT_ID

Set default context ID for the session. Can be overridden by `--context-id` flag.

**Priority**: `--context-id` flag > `A2A_CONTEXT_ID` > auto-detect

**Examples**:
```sh
# Set context ID via environment
export A2A_CONTEXT_ID=my-session
postman start

# Override with flag
A2A_CONTEXT_ID=my-session postman start --context-id override-session
```

### A2A_NODE

Set node name for the current process. Used by `create-draft --from` fallback and watchdog detection.

**Examples**:
```sh
# Start as watchdog node
A2A_NODE=watchdog postman start --context-id test-session

# Create draft from worker node
A2A_NODE=worker postman create-draft --to orchestrator
```

## Configuration

postman reads configuration from `config.toml` (XDG config paths or explicit `--config`).

### Routing Management

**NOTE:** Editing edges via TUI will remove comments from postman.toml.
Manual editing is recommended for preserving comments.

## Usage

```sh
# Start daemon
postman start --context-id <session-id> [--config path/to/config.toml]

# Create draft message
postman create-draft --to <recipient> --context-id <session-id> --from <sender>

# Show version
postman version
```