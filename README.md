# tmux-a2a-postman

tmux agent-to-agent message delivery daemon.

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