# CLI Command Reference

This page documents the supported public `tmux-a2a-postman` command surface.
The public surface is intentionally small: `start`, `stop`, `send`, `pop`,
`status`, and `--version`.

Use an explicit subcommand. Bare `tmux-a2a-postman` prints usage instead of
starting the daemon.

## 1. Command Overview

| Command     | Purpose                                              |
| ----------- | ---------------------------------------------------- |
| `start`     | Start the postman daemon                             |
| `stop`      | Stop the running daemon                              |
| `send`      | Compose and send a message in one step               |
| `pop`       | Read and archive the next inbox message              |
| `status`    | Show the current runtime status                      |
| `--version` | Print the installed version string                   |

Compatibility and diagnostic helpers are internal. They are not part of CLI
dispatch or the public operator contract.

## 2. Global Flags

The following flags are defined at the root level:

| Flag           | Type   | Default | Description                                  |
| -------------- | ------ | ------- | -------------------------------------------- |
| `--version`    | bool   | false   | Print version and exit                       |
| `--help`       | bool   | false   | Print help and exit                          |
| `--no-tui`     | bool   | false   | Run headless                                 |
| `--config`     | string | ""      | Path to config file                          |
| `--log-file`   | string | ""      | Path to daemon log file                      |
| `--base-dir`   | string | ""      | Override state directory (`POSTMAN_HOME`)    |
| `--state-home` | string | ""      | Override `XDG_STATE_HOME`                    |

Public flag tables omit internal compatibility flags.

## 3. Daemon Management

### 3.1. start

```text
tmux-a2a-postman start [global flags]
```

Starts the daemon for the current Unix user. The public deployment model is one
daemon process per Unix user; the daemon owns the tmux sessions it observes.

### 3.2. stop

```text
tmux-a2a-postman stop [--session NAME] [--config PATH] [--timeout N]
```

| Flag        | Type   | Default | Description                        |
| ----------- | ------ | ------- | ---------------------------------- |
| `--session` | string | ""      | tmux session name                  |
| `--config`  | string | ""      | Path to config file                |
| `--timeout` | int    | 10      | Seconds to wait for daemon exit    |

`stop` is idempotent: it exits successfully when no matching daemon is running.

## 4. Messaging

### 4.1. send

```text
tmux-a2a-postman send --to NODE --body TEXT [--json] [--params ...]
```

`send` is the primary command for agent-to-agent messaging. It composes a
message, submits it to the daemon when possible, and reports the strongest
outcome observed during a short confirmation window.

| Flag                | Type   | Default | --params? | Description                                 |
| ------------------- | ------ | ------- | --------- | ------------------------------------------- |
| `--to`              | string | ""      | Yes       | Recipient node name                         |
| `--body`            | string | ""      | Yes       | Message body                                |
| `--idempotency-key` | string | ""      | Yes       | Idempotency token for deduplication         |
| `--json`            | bool   | false   | Yes       | Output JSON                                 |
| `--params`          | string | ""      | N/A       | Shorthand or JSON parameters                |
| `--session`         | string | ""      | No        | tmux session name                           |
| `--config`          | string | ""      | No        | Path to config file                         |

JSON output:

```text
{"sent":"20240101-120000-xxxx-from-worker.md","status":"processed"}
{"sent":"20240101-120000-xxxx-from-worker.md","status":"queued"}
```

`processed` means the CLI observed daemon-side handling. `queued` means local
handoff succeeded, but daemon-side processing was not observed before the
confirmation window closed.

### 4.2. pop

```text
tmux-a2a-postman pop [--peek] [--json] [--params ...] [--file FILENAME]
```

`pop` reads the next unread inbox message for the current pane title. It
archives the message after reading unless `--peek` or `--file` is used.

| Flag       | Type   | Default | --params? | Description                                  |
| ---------- | ------ | ------- | --------- | -------------------------------------------- |
| `--peek`   | bool   | false   | Yes       | Read without archiving                       |
| `--json`   | bool   | false   | Yes       | Output JSON                                  |
| `--params` | string | ""      | N/A       | Shorthand or JSON parameters                 |
| `--file`   | string | ""      | No        | Print one inbox message by filename          |
| `--config` | string | ""      | No        | Path to config file                          |

JSON output uses a two-shape contract:

```text
{}
{"id":"filename.md","from":"...","to":"...","body":"...","timestamp":"..."}
```

Test for the `id` field before treating the response as a message.

## 5. Status

### 5.1. status

```text
tmux-a2a-postman status [--json] [--session NAME] [--config PATH]
```

Human output is the compact all-session runtime line:

```text
[0]🔷🔵:🟢 [1]🔴
```

`--json` returns the canonical all-session status payload:

```json
{
  "schema_version": 1,
  "context_id": "20240101-...",
  "daemon_owner": {
    "context_id": "20240101-...",
    "session_name": "review"
  },
  "sessions": [
    {
      "context_id": "20240101-...",
      "session_name": "review",
      "node_count": 4,
      "visible_state": "composing",
      "compact": "🟣",
      "queues": {
        "post_count": 0,
        "inbox_count": 2,
        "waiting_count": 1,
        "dead_letter_count": 0
      },
      "nodes": [
        {
          "name": "worker",
          "pane_id": "%11",
          "pane_state": "ready",
          "waiting_state": "composing",
          "visible_state": "composing",
          "inbox_count": 2,
          "waiting_count": 1,
          "current_command": "claude"
        }
      ],
      "windows": [
        {"index": "0", "nodes": [{"name": "worker"}]}
      ],
      "input_locks": [
        {
          "pane_id": "%11",
          "node_name": "worker",
          "owner": "tmux-delivery:review:worker",
          "expires_at": "2024-01-01T12:00:30Z"
        }
      ]
    }
  ]
}
```

Use `schema_version` before parsing, `daemon_owner` to identify the runtime
owner, `sessions[*].nodes[*].visible_state` for per-node state, `queues` for
mailbox backlogs, and `input_locks` for active pane input broker leases.
`sessions[*].compact` for compact display tokens.

| Flag        | Type   | Default | --params? | Description                       |
| ----------- | ------ | ------- | --------- | --------------------------------- |
| `--json`    | bool   | false   | Yes       | Output canonical status JSON      |
| `--params`  | string | ""      | N/A       | Shorthand or JSON parameters      |
| `--session` | string | ""      | No        | tmux session name                 |
| `--config`  | string | ""      | No        | Path to config file               |

## 6. Configuration

Configuration uses two file formats: TOML for structural settings and Markdown
for templates. Both live in `$XDG_CONFIG_HOME/tmux-a2a-postman/`, with optional
project-local overrides in `.tmux-a2a-postman/`.

Core public settings:

| Field                      | Purpose                                  |
| -------------------------- | ---------------------------------------- |
| `edges`                    | Bidirectional routes between nodes       |
| `ui_node`                  | Human-facing node for daemon-originated mail |
| `message_footer`           | Footer appended to stored `send` mail    |
| `notification_template`    | Pane hint rendered when mail arrives     |
| `min_delivery_gap_seconds` | Same-route delivery gap for duplicate control |
| `retention_period_days`    | Inactive runtime cleanup window          |

Edge syntax:

```toml
[postman]
edges = [
  "orchestrator -- worker",
  "orchestrator -- critic",
]
```

## 7. Runtime Directory Lifecycle

Base directory resolution, in priority order:

1. `$POSTMAN_HOME`
2. `base_dir` in config
3. `$XDG_STATE_HOME/tmux-a2a-postman`

Runtime layout:

```text
{baseDir}/
  lock/                 # active coordination state
  {contextId}/
    postman.log
    pane-activity.json
    {sessionName}/
      postman.pid
      post/             # daemon input queue
      inbox/{node}/     # delivered unread messages
      read/             # archived messages
      dead-letter/      # unroutable messages
      waiting/          # per-node waiting state files
```

`retention_period_days` controls cleanup of inactive runtime state. Unknown
entries are preserved by default instead of being pruned by name.

## 8. --params

`--params` lets callers set public command flags via one argument.

Shorthand:

```text
tmux-a2a-postman send --params 'to=worker,body=hello'
```

JSON:

```text
tmux-a2a-postman send --params '{"to":"worker","body":"hello"}'
```

Explicit CLI flags override `--params` values.

Always-excluded flags:

| Flag      | Reason                          |
| --------- | ------------------------------- |
| `config`  | Avoid config path injection      |
| `session` | Avoid session hijack ambiguity   |
| `file`    | Avoid arbitrary file path input  |

## 9. Version

```text
tmux-a2a-postman --version
```

The command prints the version embedded at build time. Nix builds may show a
version derived from the package metadata rather than from the local Git tag.
