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