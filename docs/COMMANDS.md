# CLI Command Reference

Full reference for all `tmux-a2a-postman` commands, flags, and behaviors.

Run `tmux-a2a-postman schema <command>` at any time to get the machine-readable
JSON Schema for a command's `--params`-settable options.

## 1. Command Overview

| Command                    | Purpose                                              |
| -------------------------- | ---------------------------------------------------- |
| `start`                    | Start the postman daemon (interactive single-column TUI) |
| `stop`                     | Stop the running daemon                              |
| `send`                     | Compose and send a message in one step               |
| `pop`                      | Read and archive the next inbox message              |
| `read`                     | List inbox messages, archived messages, or dead-letters |
| `get-health`               | Canonical JSON health report for all nodes in the session |
| `get-health-oneline`       | One-line formatter over the canonical health payload |
| `get-context-id`           | Print the active context ID                          |
| `supervisor-drain`         | Drain dead-letter queue after session rollback       |
| `bind`                     | Manage sidecar bindings (register/assign/deactivate/rebind) |
| `schema`                   | Print JSON Schema for a command or config            |
| `help`                     | Print help topics                                    |

The default operator surface is `send`, `pop`, `bind`, `get-health`, and
`get-health-oneline`.

Lifecycle and recovery commands (`start`, `stop`, `get-context-id`, and
similar helpers) remain discoverable, but they do not define the default
beginner/operator loop.

## 1.1. Migration Map

Older default-path names are now historical only:

| Older name                   | Current path           | Role in the reduced surface |
| ---------------------------- | ---------------------- | --------------------------- |
| `send-message`               | `send`                 | Default operator command |
| `get-session-health`         | `get-health`           | Canonical JSON status payload |
| `get-session-status-oneline` | `get-health-oneline`   | Pure one-line formatter over `get-health` |

## 2. Daemon Management

### 2.1. Global Flags

The following flags are defined at the root level and apply to all commands:

| Flag            | Type   | Default | Description                                            |
| --------------- | ------ | ------- | ------------------------------------------------------ |
| `--no-tui`      | bool   | false   | Run headless (no TUI surface; for CI or automated environments) |
| `--context-id`  | string | ""      | Override context ID (auto-detected from tmux session)  |
| `--config`      | string | ""      | Path to config file (auto-detected from XDG_CONFIG_HOME)|
| `--log-file`    | string | ""      | Path to log file (defaults to state dir log)           |
| `--base-dir`    | string | ""      | Override state directory (sets POSTMAN_HOME)           |
| `--state-home`  | string | ""      | Override XDG_STATE_HOME                                |

### 2.2. start

```text
tmux-a2a-postman start [global flags]
```

Starts the postman daemon. Accepts all global flags (Section 2.1). No
start-specific flags beyond the globals.

### 2.3. stop

```text
tmux-a2a-postman stop [--session NAME] [--config PATH] [--timeout N]
```

| Flag        | Type   | Default | Description                               |
| ----------- | ------ | ------- | ----------------------------------------- |
| `--session` | string | ""      | tmux session name (auto-detected)         |
| `--config`  | string | ""      | Path to config file                       |
| `--timeout` | int    | 10      | Seconds to wait for daemon to exit        |

Sends SIGTERM and polls until the process exits or timeout expires.

## 3. Messaging Commands

### 3.1. send

```text
tmux-a2a-postman send --to NODE --body TEXT [options]
```

The primary command for agent-to-agent messaging. Composes and delivers
a message atomically in one step.

| Flag                | Type   | Default | --params? | Description                                   |
| ------------------- | ------ | ------- | --------- | --------------------------------------------- |
| `--to`              | string | ""      | Yes       | Recipient node name (required)                |
| `--body`            | string | ""      | Yes       | Message body (required)                       |
| `--idempotency-key` | string | ""      | Yes       | Idempotency token for deduplication           |
| `--json`            | bool   | false   | Yes       | Output JSON (see below)                       |
| `--params`          | string | ""      | N/A       | Shorthand or JSON parameters (see Section 9)  |
| `--context-id`      | string | ""      | No        | Context ID (auto-detected; excluded)          |
| `--session`         | string | ""      | No        | tmux session name (auto-detected; excluded)   |
| `--config`          | string | ""      | No        | Path to config file (excluded)                |
| `--from`            | string | ""      | No        | Phony sender node (sidecar only; excluded)    |
| `--bindings`        | string | ""      | No        | Path to bindings.toml (required with --from)  |

**`--json` output shapes:**

```text
{"sent": "20240101-120000-xxxx-from-worker.md"}
```

### 3.2. pop

```text
tmux-a2a-postman pop [--peek] [--json] [--params ...] [--context-id ID] [--file FILENAME]
```

Reads the next unread inbox message. Archives it after reading unless
`--peek` is used. `--file` performs a non-destructive cross-context read
without archiving.

| Flag           | Type   | Default | --params? | Description                                        |
| -------------- | ------ | ------- | --------- | -------------------------------------------------- |
| `--peek`       | bool   | false   | Yes       | Read without archiving (non-destructive)           |
| `--json`       | bool   | false   | Yes       | Output JSON (two-shape; see below)                 |
| `--params`     | string | ""      | N/A       | Shorthand or JSON parameters (see Section 9)       |
| `--context-id` | string | ""      | No        | Context ID (excluded from --params)                |
| `--file`       | string | ""      | No        | Print specific inbox message by filename; cross-context, non-destructive (excluded from --params) |

**`--json` output shapes (two-shape contract):**

```text
{}                                                    # empty inbox sentinel
{"id":"filename.md","from":"...","to":"...","body":"...","timestamp":"..."}
```

Test the `id` field to distinguish the two shapes. Never assume a non-empty
response means a message was present.

## 4. Inbox Management Commands

### 4.1. read

```text
tmux-a2a-postman read [--json] [--archived [--file F]] [--dead-letters [--resend-oldest | --file F]] [--params ...]
```

Lists inbox messages (default), archived messages, or dead-letter messages
depending on the flags provided.

| Flag              | Type   | Default | --params? | Description                                                                       |
| ----------------- | ------ | ------- | --------- | --------------------------------------------------------------------------------- |
| `--json`          | bool   | false   | Yes       | Output JSON (shape depends on mode; see below)                                    |
| `--archived`      | bool   | false   | Yes       | List archived messages in read/ (self-filtered to calling node)                   |
| `--dead-letters`  | bool   | false   | Yes       | List dead-letter messages (metadata only, filenames hidden)                       |
| `--resend-oldest` | bool   | false   | Yes       | Resend the oldest dead-letter; requires `--dead-letters`                          |
| `--file`          | string | ""      | No        | With `--archived`: print specific archived message; with `--dead-letters`: resend specific named dead-letter (excluded from --params) |
| `--params`        | string | ""      | N/A       | Shorthand or JSON parameters (see Section 9)                                      |

**Mutual exclusions:**

- `--archived` and `--dead-letters` together → error
- `--resend-oldest` without `--dead-letters` → error

**`--json` output shapes by mode:**

```text
(default)           {"files": [...]}
--archived          {"messages": [{"file","from","to","timestamp"}]}
--dead-letters      {"messages": [{"from","to","timestamp"}]}
```

**Note:** `--archived` self-filters to messages addressed to the calling node
(the node whose pane title matches the current tmux pane). Raw filenames for
dead-letter messages are never exposed (`--dead-letters` metadata only).

## 5. Dead-Letter Commands

### 5.1. supervisor-drain

```text
tmux-a2a-postman supervisor-drain [--context-id ID] [--config PATH]
```

Phase 3 → Phase 2 rollback drain procedure. Redelivers eligible dead-letters
and quarantines ineligible ones.

| Flag           | Type   | Default | Description                                  |
| -------------- | ------ | ------- | -------------------------------------------- |
| `--context-id` | string | ""      | Context ID (auto-resolved from tmux session) |
| `--config`     | string | ""      | Path to config file                          |

Does not accept `--params`.

## 6. bind Subcommand

```text
tmux-a2a-postman bind <subcommand> [flags]
```

Manages sidecar bindings in a `bindings.toml` file. Used when a node sends
messages on behalf of another node via `--from`. Does not accept `--params`.

### 6.1. bind register

Appends an unassigned binding (active=false, no session).

```text
tmux-a2a-postman bind register --file PATH --channel-id ID --node-name NAME \
  --context-id ID --permitted-senders sender1,sender2
```

| Flag                  | Required | Description                              |
| --------------------- | -------- | ---------------------------------------- |
| `--file`              | Yes      | Path to bindings.toml                    |
| `--channel-id`        | Yes      | Channel ID for the binding               |
| `--node-name`         | Yes      | Node name to register                    |
| `--context-id`        | Yes      | Context ID for the binding               |
| `--permitted-senders` | Yes      | Comma-separated list of permitted senders|

### 6.2. bind assign

Activates a registered binding and sets session/pane matching fields.

```text
tmux-a2a-postman bind assign --file PATH --node-name NAME --session-name NAME \
  [--pane-title TITLE] [--pane-node-name NAME]
```

| Flag               | Required | Description                              |
| ------------------ | -------- | ---------------------------------------- |
| `--file`           | Yes      | Path to bindings.toml                    |
| `--node-name`      | Yes      | Node name to activate                    |
| `--session-name`   | Yes      | tmux session name                        |
| `--pane-title`     | No*      | Pane title for matching                  |
| `--pane-node-name` | No*      | Pane node name for matching              |

*At least one of `--pane-title` or `--pane-node-name` is required.

### 6.3. bind deactivate

Sets active=false for the named node binding.

```text
tmux-a2a-postman bind deactivate --file PATH --node-name NAME
```

| Flag          | Required | Description           |
| ------------- | -------- | --------------------- |
| `--file`      | Yes      | Path to bindings.toml |
| `--node-name` | Yes      | Node name to deactivate|

### 6.4. bind rebind

Full field update on an existing binding.

```text
tmux-a2a-postman bind rebind --file PATH --node-name NAME [--session-name NAME]
  [--pane-title TITLE] [--pane-node-name NAME] [--active BOOL]
  [--permitted-senders LIST]
```

| Flag                  | Required | Description                                    |
| --------------------- | -------- | ---------------------------------------------- |
| `--file`              | Yes      | Path to bindings.toml                          |
| `--node-name`         | Yes      | Node name to rebind                            |
| `--session-name`      | No       | New session name                               |
| `--pane-title`        | No       | New pane title                                 |
| `--pane-node-name`    | No       | New pane node name                             |
| `--active`            | No       | Active state (default true)                    |
| `--permitted-senders` | No       | Comma-separated senders (replaces existing)    |

## 7. Session Inspection Commands

### 7.1. get-health

```text
tmux-a2a-postman get-health [--context-id ID] [--session NAME] [--config PATH]
```

Always outputs JSON. There is no `--json` flag. Does not accept `--params`.

| Flag           | Type   | Default | Description                                        |
| -------------- | ------ | ------- | -------------------------------------------------- |
| `--context-id` | string | ""      | Context ID (auto-resolved from tmux session)       |
| `--session`    | string | ""      | tmux session name (optional, auto-detect if in tmux) |
| `--config`     | string | ""      | Path to config file                                |

**Output shape:**

```text
{
  "context_id": "20240101-...",
  "session_name": "review",
  "node_count": 4,
  "visible_state": "composing",
  "nodes": [
    {
      "name": "worker",
      "pane_id": "%11",
      "pane_state": "active",
      "waiting_state": "composing",
      "visible_state": "composing",
      "inbox_count": 2,
      "waiting_count": 1,
      "current_command": "claude"
    },
    ...
  ],
  "windows": [
    {"index": "0", "nodes": [{"name": "worker"}, {"name": "critic"}]}
  ]
}
```

Use top-level `visible_state` for the session summary, `nodes[*].visible_state`
for per-node status, and `windows` for the canonical node ordering consumed by
`get-health-oneline` and the default TUI.

### 7.2. get-health-oneline

```text
tmux-a2a-postman get-health-oneline [--json] [--params ...] [--context-id ID] [--session NAME] [--config PATH]
```

One-line status string suitable for embedding in a tmux status-bar. Formats the
resolved `get-health` payload for one session; it does not do independent
session discovery or separate overlay derivation.

| Flag           | Type   | Default | --params? | Description                                         |
| -------------- | ------ | ------- | --------- | --------------------------------------------------- |
| `--json`       | bool   | false   | Yes       | Output JSON: `{"status": "[0]●●●●"}`                |
| `--params`     | string | ""      | N/A       | Shorthand or JSON parameters                        |
| `--context-id` | string | ""      | No        | Context ID (auto-resolved from tmux session)        |
| `--session`    | string | ""      | No        | tmux session name (optional, auto-detect if in tmux) |
| `--config`     | string | ""      | No        | Path to config file                                 |

### 7.3. get-context-id

```text
tmux-a2a-postman get-context-id [--json] [--params ...] [--session NAME] [--config PATH]
```

| Flag        | Type   | Default | --params? | Description                                       |
| ----------- | ------ | ------- | --------- | ------------------------------------------------- |
| `--json`    | bool   | false   | Yes       | Output JSON: `{"context_id": "..."}`              |
| `--params`  | string | ""      | N/A       | Shorthand or JSON parameters                      |
| `--session` | string | ""      | No        | tmux session name (excluded from --params)        |
| `--config`  | string | ""      | No        | Path to config file (excluded from --params)      |

## 8. schema Subcommand

```text
tmux-a2a-postman schema [COMMAND] [--nodes-dir]
```

Prints a JSON Schema describing the options or output shape of a command.
Use this to discover `--params`-settable flags and their types at runtime.
Do not hardcode flag lists in agent role templates — query `schema` instead.

**`--nodes-dir` flag:**

```text
tmux-a2a-postman schema --nodes-dir
# {"xdg":"...","project_local":"..."}
```

Outputs the XDG and project-local node template directories as JSON.
Can be combined with a command argument or used alone.

**Commands with schema support:**

| Argument                   | Describes                                     |
| -------------------------- | --------------------------------------------- |
| (none)                     | `postman.toml` config properties              |
| `send`                     | `send` `--params` scope                       |
| `pop`                      | `pop` `--params` scope                        |
| `read`                     | `read` `--params` scope                       |
| `get-context-id`           | `get-context-id` `--params` scope             |
| `get-health-oneline`       | `get-health-oneline` `--params` scope         |
| `get-health`               | `get-health` output shape                     |

**Important:** Schema properties show only `--params`-settable flags.
Always-excluded flags (`context-id`, `config`, `session`, `from`, `bindings`,
`file`) are intentionally absent from schema output.

## 9. --params Flag

The `--params` flag is available on all messaging and inbox commands. It lets
callers set command options via a single argument instead of multiple flags.

### 9.1. Forms

**Shorthand (k=v,k=v):**

```text
tmux-a2a-postman send --params 'to=worker,body=hello'
```

Values may contain `=` characters (split on first `=` only). Values
containing commas require JSON form.

**JSON:**

```text
tmux-a2a-postman send --params '{"to":"worker","body":"hello"}'
```

Detection: if the trimmed value starts with `{`, it is parsed as JSON;
otherwise shorthand is assumed.

### 9.2. Precedence

Explicit CLI flags override `--params` values. Use this to override a param:

```text
tmux-a2a-postman send --params 'to=worker,body=hello' --body override
# sends body="override", to="worker"
```

### 9.3. JSON Number Preservation

JSON numeric values are preserved as-is using `json.Decoder.UseNumber()`.
Large integers are not converted to scientific notation:

```text
--params '{"count":1000000}'  →  count flag gets "1000000", not "1e+06"
```

Floats are also preserved: `3.14` → `"3.14"`.

### 9.4. Always-Excluded Flags

The following flags are never settable via `--params` across all commands.
Attempting to set them returns a hard error.

| Flag           | Reason                                      |
| -------------- | ------------------------------------------- |
| `context-id`   | Security: context redirect risk             |
| `config`       | Security: config path injection             |
| `session`      | Security: session hijack risk               |
| `from`         | Security: sender identity spoofing          |
| `bindings`     | Security: binding injection                 |
| `file`         | Security: arbitrary filesystem path         |

### 9.5. Error Messages

| Scenario                         | Error                                                    |
| -------------------------------- | -------------------------------------------------------- |
| Excluded flag in `--params`      | `--params: field "X" is not settable via --params`       |
| Non-scalar value (array)         | `--params: field "X" must be scalar, got []interface {}`           |
| Non-scalar value (object)        | `--params: field "X" must be scalar, got map[string]interface {}`  |
| Null JSON value                  | `--params: field "X" must be a scalar value, not null`   |
| Missing `=` in shorthand         | `--params: invalid shorthand pair "X": missing = separator (values containing commas require JSON form: --params '{"key":"val,with,commas"}')` |
| Invalid JSON                     | `--params JSON parse error: <decode error>`              |
| Unknown flag name                | `--params: invalid value for "X": no such flag -X`       |

### 9.7. Key Case Sensitivity

`--params` keys are matched case-sensitively against flag names. Flag names
use hyphen-lowercase form (e.g., `idempotency-key`, `no-tui`). Uppercase or
mixed-case keys will not match and produce an "unknown flag" error:

```text
--params 'To=worker'   # ERROR: no such flag -To (use "to")
--params 'to=worker'   # OK
```

### 9.8. --params Scope Discovery

To see exactly which flags are settable via `--params` for any command:

```text
tmux-a2a-postman schema send           # required: ["to","body"]
tmux-a2a-postman schema pop            # no required fields
tmux-a2a-postman schema               # postman.toml config schema
```

The `required` array in schema output lists flags that must be provided
(via explicit flag or `--params`).

## 10. --json Flag

All messaging and inbox commands accept `--json` as a `--params`-settable
flag. Output goes to stdout; errors go to stderr.

| Command                    | Empty / no-result shape | Populated shape (keys)                                       |
| -------------------------- | ----------------------- | ------------------------------------------------------------ |
| `send`                     | N/A                     | `{"sent": "filename.md"}`                                    |
| `pop`                      | `{}`                    | `{"id","from","to","body","timestamp"}`                      |
| `read` (default)           | `{"files":[]}`          | `{"files":["...","..."]}`                                    |
| `read --archived`          | `{"messages":[]}`       | `{"messages":[{"file","from","to","timestamp"}]}`            |
| `read --dead-letters`      | `{"messages":[]}`       | `{"messages":[{"from","to","timestamp"}]}`                   |
| `get-context-id`           | N/A                     | `{"context_id":"..."}`                                       |
| `schema --nodes-dir`       | N/A                     | `{"xdg":"...","project_local":"..."}`                        |
| `get-health-oneline`       | N/A                     | `{"status":"[0]●●●●"}`                                       |
| `get-health`               | always JSON (no flag)   | `{"context_id","session_name","node_count","visible_state","nodes":[...],"windows":[...]}` |

**Two-shape contract:** `pop` returns `{}` for the empty case.
Always test for the presence of the `id` key before treating the result as a
message.

## 11. help Subcommand

```text
tmux-a2a-postman help [TOPIC]
```

| Topic                  | Content                                  |
| ---------------------- | ---------------------------------------- |
| (none)                 | List available topics                    |
| `messaging`            | Message flow and node communication      |
| `directories`          | State directory layout                   |
| `config`               | Configuration file structure             |
| `commands`             | Command list with one-line descriptions  |
