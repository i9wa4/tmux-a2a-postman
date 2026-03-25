# CLI Command Reference

Full reference for all `tmux-a2a-postman` commands, flags, and behaviors.

Run `tmux-a2a-postman schema <command>` at any time to get the machine-readable
JSON Schema for a command's `--params`-settable options.

## 1. Command Overview

| Command                    | Purpose                                              |
| -------------------------- | ---------------------------------------------------- |
| `start`                    | Start the postman daemon (interactive TUI)           |
| `stop`                     | Stop the running daemon                              |
| `send-message`             | Compose and send a message in one step               |
| `create-draft`             | Create a draft message (optionally send immediately) |
| `send`                     | Move a draft to the post queue for delivery          |
| `next`                     | Read and archive the next inbox message              |
| `read`                     | List inbox message file paths                        |
| `count`                    | Count unread inbox messages                          |
| `archive`                  | Move inbox messages to the read archive              |
| `resend`                   | Move a dead-letter back to the post queue            |
| `list-dead-letters`        | List undeliverable messages                          |
| `list-archived-messages`   | List archived (read) messages                        |
| `show-inbox-message`       | Print a specific inbox message                       |
| `show-archived-message`    | Print a specific archived message                    |
| `get-session-health`       | JSON health report for all nodes in the session      |
| `get-session-status-oneline` | One-line status string for tmux status-bar         |
| `get-context-id`           | Print the active context ID                          |
| `get-nodes-dir`            | Print XDG and project-local node template dirs       |
| `supervisor-drain`         | Drain dead-letter queue after session rollback       |
| `bind`                     | Manage sidecar bindings (register/assign/deactivate/rebind) |
| `schema`                   | Print JSON Schema for a command or config            |
| `help`                     | Print help topics                                    |

## 2. Daemon Management

### 2.1. Global Flags

The following flags are defined at the root level and apply to all commands:

| Flag            | Type   | Default | Description                                            |
| --------------- | ------ | ------- | ------------------------------------------------------ |
| `--no-tui`      | bool   | false   | Run headless (no TUI; for CI or automated environments)|
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

### 3.1. send-message

```text
tmux-a2a-postman send-message --to NODE --body TEXT [options]
```

The primary command for agent-to-agent messaging. Composes and delivers
a message atomically (create-draft + send in one step).

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

### 3.2. create-draft

```text
tmux-a2a-postman create-draft --to NODE [--body TEXT] [options]
```

Creates a draft message file. Use `--send` to deliver atomically, or run
`send` separately.

| Flag                | Type   | Default | --params? | Description                                           |
| ------------------- | ------ | ------- | --------- | ----------------------------------------------------- |
| `--to`              | string | ""      | Yes       | Recipient node name (required)                        |
| `--idempotency-key` | string | ""      | Yes       | Idempotency token for deduplication                   |
| `--json`            | bool   | false   | Yes       | Output JSON (see below)                               |
| `--params`          | string | ""      | N/A       | Shorthand or JSON parameters (see Section 9)          |
| `--body`            | string | ""      | No        | Message body; excluded: contains `{{PLACEHOLDER}}`    |
| `--send`            | bool   | false   | No        | Send immediately after creating draft; excluded       |
| `--context-id`      | string | ""      | No        | Context ID (excluded)                                 |
| `--session`         | string | ""      | No        | tmux session name (excluded)                          |
| `--config`          | string | ""      | No        | Path to config file (excluded)                        |
| `--from`            | string | ""      | No        | Phony sender node (sidecar only; excluded)            |
| `--bindings`        | string | ""      | No        | Path to bindings.toml (required with --from)          |

**`--json` output shapes:**

```text
{"draft": "20240101-120000-xxxx-from-worker.md"}   # draft only
{"sent":  "20240101-120000-xxxx-from-worker.md"}   # --send used
```

**Note on `--body` exclusion:** `--body` is excluded from `--params` for
`create-draft` because the body template may contain `{{PLACEHOLDER}}`
tokens. Pass `--body` as an explicit flag instead.

### 3.3. send

```text
tmux-a2a-postman send FILENAME [FILENAME ...]
```

Positional arguments only. Moves one or more draft files to the post queue
for daemon delivery. Plain filenames are globbed as
`{base}/*/*/draft/{filename}`. Does not accept `--params`.

### 3.4. next

```text
tmux-a2a-postman next [--peek] [--json] [--params ...] [--context-id ID]
```

Reads the next unread inbox message. Archives it after reading unless
`--peek` is used.

| Flag           | Type   | Default | --params? | Description                                        |
| -------------- | ------ | ------- | --------- | -------------------------------------------------- |
| `--peek`       | bool   | false   | Yes       | Read without archiving (non-destructive)           |
| `--json`       | bool   | false   | Yes       | Output JSON (two-shape; see below)                 |
| `--params`     | string | ""      | N/A       | Shorthand or JSON parameters (see Section 9)       |
| `--context-id` | string | ""      | No        | Context ID (excluded from --params)                |

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
tmux-a2a-postman read [--json] [--params ...]
```

Lists inbox message file paths for the current node.

| Flag       | Type | Default | --params? | Description                             |
| ---------- | ---- | ------- | --------- | --------------------------------------- |
| `--json`   | bool | false   | Yes       | Output JSON: `{"files": [...]}`         |
| `--params` | string | ""    | N/A       | Shorthand or JSON parameters            |

### 4.2. count

```text
tmux-a2a-postman count [--json] [--params ...]
```

| Flag       | Type   | Default | --params? | Description                             |
| ---------- | ------ | ------- | --------- | --------------------------------------- |
| `--json`   | bool   | false   | Yes       | Output JSON: `{"count": N}`             |
| `--params` | string | ""      | N/A       | Shorthand or JSON parameters            |

### 4.3. archive

```text
tmux-a2a-postman archive FILENAME [FILENAME ...]
```

Positional arguments only. Moves inbox message files to the `read/`
archive directory. Plain filenames are globbed as
`{base}/*/*/inbox/*/{filename}`. Does not accept `--params`.

### 4.4. show-inbox-message

```text
tmux-a2a-postman show-inbox-message FILENAME
```

Positional argument only. Prints the contents of a specific inbox message.
Filename must not contain path separators. Does not accept `--params`.

### 4.5. list-archived-messages

```text
tmux-a2a-postman list-archived-messages [--json] [--params ...]
```

| Flag       | Type   | Default | --params? | Description                                                    |
| ---------- | ------ | ------- | --------- | -------------------------------------------------------------- |
| `--json`   | bool   | false   | Yes       | Output JSON: `{"messages": [{"file","from","to"}]}`            |
| `--params` | string | ""      | N/A       | Shorthand or JSON parameters                                   |

### 4.6. show-archived-message

```text
tmux-a2a-postman show-archived-message FILENAME
```

Positional argument only. Prints the contents of a specific archived message.
Filename must not contain path separators. Does not accept `--params`.

## 5. Dead-Letter Commands

### 5.1. list-dead-letters

```text
tmux-a2a-postman list-dead-letters [--json] [--params ...]
```

| Flag       | Type   | Default | --params? | Description                                                     |
| ---------- | ------ | ------- | --------- | --------------------------------------------------------------- |
| `--json`   | bool   | false   | Yes       | Output JSON: `{"messages": [{"from","to","timestamp"}]}`        |
| `--params` | string | ""      | N/A       | Shorthand or JSON parameters                                    |

**Note:** Raw filenames are never exposed in the JSON output. Metadata only.

### 5.2. resend

```text
tmux-a2a-postman resend [--oldest | --file PATH] [--json] [--params ...]
```

Moves a dead-letter file back to the post queue for redelivery.
`--oldest` and `--file` are mutually exclusive.

| Flag           | Type   | Default | --params? | Description                                                     |
| -------------- | ------ | ------- | --------- | --------------------------------------------------------------- |
| `--oldest`     | bool   | false   | Yes       | Resend the lexicographically oldest dead-letter                 |
| `--json`       | bool   | false   | Yes       | Output JSON (two-shape; see below)                              |
| `--params`     | string | ""      | N/A       | Shorthand or JSON parameters                                    |
| `--file`       | string | ""      | No        | Path to dead-letter file (excluded from --params)               |
| `--context-id` | string | ""      | No        | Context ID (excluded from --params)                             |
| `--config`     | string | ""      | No        | Path to config file (excluded from --params)                    |

**`--json` output shapes (two-shape contract):**

```text
{}                                              # empty dead-letter queue
{"from":"...","to":"...","timestamp":"..."}     # resent message metadata
```

Test for an empty object to detect an empty queue.

### 5.3. supervisor-drain

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

### 7.1. get-session-health

```text
tmux-a2a-postman get-session-health [--context-id ID] [--config PATH]
```

Always outputs JSON. There is no `--json` flag. Does not accept `--params`.

| Flag           | Type   | Default | Description                                  |
| -------------- | ------ | ------- | -------------------------------------------- |
| `--context-id` | string | ""      | Context ID (auto-resolved from tmux session) |
| `--config`     | string | ""      | Path to config file                          |

**Output shape:**

```text
{
  "context_id": "20240101-...",
  "node_count": 4,
  "nodes": [
    {"name": "worker", "inbox_count": 2, "waiting_count": 0},
    ...
  ]
}
```

Use `nodes[*].waiting_count > 0` to detect delivery stalls.

### 7.2. get-session-status-oneline

```text
tmux-a2a-postman get-session-status-oneline [--json] [--params ...]
```

One-line status string suitable for embedding in a tmux status-bar.

| Flag       | Type   | Default | --params? | Description                                         |
| ---------- | ------ | ------- | --------- | --------------------------------------------------- |
| `--json`   | bool   | false   | Yes       | Output JSON: `{"status": "[1]●●●●"}`                |
| `--params` | string | ""      | N/A       | Shorthand or JSON parameters                        |

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

### 7.4. get-nodes-dir

```text
tmux-a2a-postman get-nodes-dir [--json] [--params ...]
```

| Flag       | Type   | Default | --params? | Description                                                |
| ---------- | ------ | ------- | --------- | ---------------------------------------------------------- |
| `--json`   | bool   | false   | Yes       | Output JSON: `{"xdg": "...", "project_local": "..."}`      |
| `--params` | string | ""      | N/A       | Shorthand or JSON parameters                               |

## 8. schema Subcommand

```text
tmux-a2a-postman schema [COMMAND]
```

Prints a JSON Schema describing the options or output shape of a command.
Use this to discover `--params`-settable flags and their types at runtime.
Do not hardcode flag lists in agent role templates — query `schema` instead.

**Commands with schema support:**

| Argument                   | Describes                                |
| -------------------------- | ---------------------------------------- |
| (none)                     | `postman.toml` config properties         |
| `send-message` or `send`   | `send-message` `--params` scope (note: `send` is also a subcommand; `schema send` refers to `send-message` schema, not the `send` subcommand) |
| `create-draft`             | `create-draft` `--params` scope          |
| `next`                     | `next` `--params` scope                  |
| `count`                    | `count` `--params` scope                 |
| `read`                     | `read` `--params` scope                  |
| `resend`                   | `resend` `--params` scope                |
| `list-dead-letters`        | `list-dead-letters` `--params` scope     |
| `list-archived-messages`   | `list-archived-messages` `--params` scope|
| `get-context-id`           | `get-context-id` `--params` scope        |
| `get-nodes-dir`            | `get-nodes-dir` `--params` scope         |
| `get-session-status-oneline` | `get-session-status-oneline` `--params` scope |
| `get-session-health`       | `get-session-health` output shape        |

**Important:** Schema properties show only `--params`-settable flags.
Always-excluded flags (`context-id`, `config`, `session`, `from`, `bindings`,
`send`, `file`) are intentionally absent from schema output.

## 9. --params Flag

The `--params` flag is available on all messaging and inbox commands. It lets
callers set command options via a single argument instead of multiple flags.

### 9.1. Forms

**Shorthand (k=v,k=v):**

```text
tmux-a2a-postman send-message --params 'to=worker,body=hello'
```

Values may contain `=` characters (split on first `=` only). Values
containing commas require JSON form.

**JSON:**

```text
tmux-a2a-postman send-message --params '{"to":"worker","body":"hello"}'
```

Detection: if the trimmed value starts with `{`, it is parsed as JSON;
otherwise shorthand is assumed.

### 9.2. Precedence

Explicit CLI flags override `--params` values. Use this to override a param:

```text
tmux-a2a-postman send-message --params 'to=worker,body=hello' --body override
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
| `send`         | Semantics: triggers irreversible send (relevant to `create-draft`; other commands do not have `--send`) |
| `file`         | Security: arbitrary filesystem path         |

### 9.5. Per-Command Exclusions

| Command        | Additional Excluded Flags | Reason                          |
| -------------- | ------------------------- | ------------------------------- |
| `create-draft` | `body`                    | Body may contain placeholders   |

### 9.6. Error Messages

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
tmux-a2a-postman schema send-message   # required: ["to","body"]
tmux-a2a-postman schema next           # no required fields
tmux-a2a-postman schema               # postman.toml config schema
```

The `required` array in schema output lists flags that must be provided
(via explicit flag or `--params`).

## 10. --json Flag

All messaging and inbox commands accept `--json` as a `--params`-settable
flag. Output goes to stdout; errors go to stderr.

| Command                    | Empty / no-result shape | Populated shape (keys)                       |
| -------------------------- | ----------------------- | -------------------------------------------- |
| `send-message`             | N/A                     | `{"sent": "filename.md"}`                    |
| `create-draft`             | N/A                     | `{"draft":"..."}` or `{"sent":"..."}`        |
| `next`                     | `{}`                    | `{"id","from","to","body","timestamp"}`      |
| `read`                     | `{"files":[]}`          | `{"files":["...","..."]}`                    |
| `count`                    | `{"count":0}`           | `{"count": N}`                               |
| `list-dead-letters`        | `{"messages":[]}`       | `{"messages":[{"from","to","timestamp"}]}`   |
| `list-archived-messages`   | `{"messages":[]}`       | `{"messages":[{"file","from","to"}]}`             |
| `resend`                   | `{}`                    | `{"from","to","timestamp"}`                  |
| `get-context-id`           | N/A                     | `{"context_id":"..."}`                       |
| `get-nodes-dir`            | N/A                     | `{"xdg":"...","project_local":"..."}`        |
| `get-session-status-oneline` | N/A                   | `{"status":"[1]●●●●"}`                       |
| `get-session-health`       | always JSON (no flag)   | `{"context_id","node_count","nodes":[...]}`  |

**Two-shape contract:** `next` and `resend` return `{}` for the empty case.
Always test for the presence of the `id` (next) or `from` (resend) key before
treating the result as a message.

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
