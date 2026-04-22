# CLI Command Reference

Full reference for the supported public `tmux-a2a-postman` commands, flags, and
behaviors.

Run `tmux-a2a-postman schema <command>` at any time to get the machine-readable
JSON Schema for a command's `--params`-settable options.

## 0. How To Read This Page

Use this page as the exact public CLI reference, not as the first-time tutorial.
Use an explicit subcommand. Bare `tmux-a2a-postman` prints usage instead of
starting the daemon.

1. Start in `README.md` if you need the operator model, the beginner command
   loop, or the state-directory overview.
2. Come here when you need exact flags, `--json` output shapes, `--params`
   scope, or the current canonical command names.
3. Prefer `tmux-a2a-postman schema <command>` when a script or agent needs the
   live machine-readable flag surface instead of prose.

## 1. Command Overview

| Command                    | Purpose                                              |
| -------------------------- | ---------------------------------------------------- |
| `start`                    | Start the postman daemon (interactive single-column TUI) |
| `stop`                     | Stop the running daemon                              |
| `send`                     | Compose and send a message in one step               |
| `pop`                      | Read and archive the next inbox message              |
| `read`                     | List inbox messages, archived messages, or dead-letters |
| `todo`                     | Manage session-local owner TODO files and live summaries |
| `get-health`               | Canonical JSON health report for all nodes in the session |
| `get-health-oneline`       | Compact all-session tokens rendered from canonical health     |
| `timeline`                 | Redacted current-generation journal timeline for the session |
| `replay`                   | Read-only rebuild of journal-backed projection surfaces |
| `get-context-id`           | Print the active context ID                          |
| `schema`                   | Print JSON Schema for a command or the curated public config surface |
| `help`                     | Print help topics                                    |

The default operator surface is `send`, `pop`, `get-health`, and
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
| `get-session-status-oneline` | `get-health-oneline`   | Compact all-session tokens over canonical health |

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

### 2.4. Runtime Directory Lifecycle and Retention

Base directory resolution is unchanged:

1. `$POSTMAN_HOME`
2. `base_dir` in config
3. `$XDG_STATE_HOME/tmux-a2a-postman`

`retention_period_days` is the startup cleanup control for inactive runtime
state. The embedded default is `90`. Set it to `0` to disable the broader
retention sweep.

At daemon startup, retention cleanup only targets inactive contexts. A context
is treated as active if any session under `{baseDir}/{contextId}/` still has a
live `postman.pid`. The current daemon writes its own `postman.pid` before this
cleanup runs, so the active session is not pruned during startup.

Eligible paths inside an inactive context:

| Path | Notes |
| ---- | ----- |
| `{baseDir}/{contextId}/{sessionName}/` | Session runtime tree containing `draft/`, `post/`, `inbox/`, `read/`, `todo/`, `dead-letter/`, `waiting/`, and optional `postman.pid` |
| `{baseDir}/{contextId}/postman.log` | Context-local daemon log |
| `{baseDir}/{contextId}/pane-activity.json` | Context-local pane activity cache |

Always-preserved paths:

| Path | Reason |
| ---- | ------ |
| `{baseDir}/lock/` | Live session ownership locks |
| `{baseDir}/{contextId}/phony/` | Binding-backed inbox and dead-letter state |
| `{baseDir}/{contextId}/supervisor-memory/` | Durable supervisor memory store |
| Unknown entries | Preserved by default instead of pruning by name guesswork |

### 2.5. Journal Cutover Modes

Two public cutover flags control how canonical health and compatibility-submit
delivery move from legacy runtime state to journal-backed projection:

| Flag | Type | Default | Description |
| ---- | ---- | ------- | ----------- |
| `journal_health_cutover_enabled` | bool | false | Enable journal-backed canonical health while direct mailbox delivery remains authoritative |
| `journal_compatibility_cutover_enabled` | bool | false | Enable compatibility-submit mailbox delivery; requires `journal_health_cutover_enabled = true` |

The effective modes are:

| Mode | Config | Behavior |
| ---- | ------ | -------- |
| `legacy` | both flags false | Legacy health and direct mailbox delivery remain authoritative |
| `health-first` | health=true, compatibility=false | `get-health`, `get-health-oneline`, and the default TUI read journal-backed canonical health; `send` and `pop` still write mailbox state directly |
| `compatibility-first` | health=true, compatibility=true | Canonical health stays journal-backed and `send` / `pop` use compatibility-submit for owned sessions |
| invalid | health=false, compatibility=true | `start` rejects the config before the daemon begins running |

## 3. Messaging Commands

### 3.1. send

```text
tmux-a2a-postman send --to NODE --body TEXT [options]
```

The primary command for agent-to-agent messaging. Composes and delivers
a message, then reports the strongest outcome the CLI can observe during a
short confirmation window. `status = "processed"` means the daemon consumed
the queued `post/` file before the window closed. `status = "queued"` means
only the local handoff to `post/` was confirmed. If a matching dead-letter is
observed during that window, `send` returns an error instead of a success
payload.

| Flag                | Type   | Default | --params? | Description                                   |
| ------------------- | ------ | ------- | --------- | --------------------------------------------- |
| `--to`              | string | ""      | Yes       | Recipient node name (required)                |
| `--body`            | string | ""      | Yes       | Message body (required)                       |
| `--idempotency-key` | string | ""      | Yes       | Idempotency token for deduplication           |
| `--json`            | bool   | false   | Yes       | Output JSON (see below)                       |
| `--params`          | string | ""      | N/A       | Shorthand or JSON parameters (see Section 7)  |
| `--context-id`      | string | ""      | No        | Context ID (auto-detected; excluded)          |
| `--session`         | string | ""      | No        | tmux session name (auto-detected; excluded)   |
| `--config`          | string | ""      | No        | Path to config file (excluded)                |

**`--json` output shapes:**

```text
{"sent": "20240101-120000-xxxx-from-worker.md", "status": "processed"}
{"sent": "20240101-120000-xxxx-from-worker.md", "status": "queued"}
```

`sent` stays as the filename field for compatibility. Inspect `status` to tell
whether the CLI observed daemon-side processing or only queue submission.

### 3.2. pop

```text
tmux-a2a-postman pop [--peek] [--json] [--params ...] [--context-id ID] [--file FILENAME]
```

Reads the next unread inbox message. Archives it after reading unless
`--peek` is used. `--file` remains non-destructive; it searches across
contexts only when `--context-id` is omitted, and an explicit
`--context-id` binds lookup to that context without archiving.
When `read_context_mode = "pieces"` is enabled, bare interactive `pop`
may append one read-time `Local Runtime Context` block after the stored
message. `--peek`, `--file`, and `--json` stay raw.

| Flag           | Type   | Default | --params? | Description                                        |
| -------------- | ------ | ------- | --------- | -------------------------------------------------- |
| `--peek`       | bool   | false   | Yes       | Read without archiving (non-destructive)           |
| `--json`       | bool   | false   | Yes       | Output JSON (two-shape; see below)                 |
| `--params`     | string | ""      | N/A       | Shorthand or JSON parameters (see Section 7)       |
| `--context-id` | string | ""      | No        | Context ID (excluded from --params)                |
| `--file`       | string | ""      | No        | Print specific inbox message by filename; non-destructive. Searches across contexts only when `--context-id` is omitted, and an explicit `--context-id` binds lookup to that context (excluded from --params) |

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
| `--params`        | string | ""      | N/A       | Shorthand or JSON parameters (see Section 7)                                      |

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

### 4.2. todo

```text
tmux-a2a-postman todo summary [--json] [--context-id ID] [--config PATH]
tmux-a2a-postman todo show [--node NODE] [--context-id ID] [--config PATH]
tmux-a2a-postman todo write (--body TEXT | --file PATH) [--context-id ID] [--config PATH]
```

`todo` is the explicit live TODO coordination surface. It reads and writes
session-local owner files under `todo/{node}.md`. `summary` reports live
checkbox counts, `show` prints one owner file as stored, and `write`
atomically replaces the current node's owner file.

**`summary` flags:**

| Flag           | Type   | Default | Description |
| -------------- | ------ | ------- | ----------- |
| `--json`       | bool   | false   | Output JSON: `{"nodes":[...]}` |
| `--context-id` | string | ""      | Context ID (auto-resolved from tmux session) |
| `--config`     | string | ""      | Path to config file |

**Human output shape:**

```text
worker [-] 1/2
orchestrator [x] 1/1
messenger [·] 0/0
```

**`summary --json` output shape:**

```text
{"nodes":[{"node":"worker","token":"[-]","state":"partial","checked":1,"total":2,"exists":true,"invalid":false}]}
```

**`show` flags:**

| Flag           | Type   | Default | Description |
| -------------- | ------ | ------- | ----------- |
| `--node`       | string | ""      | Owner node to read; defaults to the current pane title |
| `--context-id` | string | ""      | Context ID (auto-resolved from tmux session) |
| `--config`     | string | ""      | Path to config file |

Missing owner files return no output and no error.

**`write` flags:**

| Flag           | Type   | Default | Description |
| -------------- | ------ | ------- | ----------- |
| `--body`       | string | ""      | Replacement document body |
| `--file`       | string | ""      | Replacement document path |
| `--context-id` | string | ""      | Context ID (auto-resolved from tmux session) |
| `--config`     | string | ""      | Path to config file |

`todo write` requires exactly one of `--body` or `--file`. The current node may
write only its own owner file.

## 5. Session Inspection Commands

### 5.1. get-health

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
  "compact": "🟣",
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
for per-node status, `compact` for the canonical compact token consumed by
`get-health-oneline`, and `windows` for the canonical window topology consumed
by the default TUI.

### 5.2. get-health-oneline

```text
tmux-a2a-postman get-health-oneline [--json] [--params ...] [--context-id ID] [--session NAME] [--config PATH]
```

One-line status string suitable for embedding in a tmux status-bar. It first
collects canonical health for all tmux sessions and all windows, then joins the
canonical `compact` token from each session-health payload. It is not a
single-session wrapper around `get-health`. Each compact token uses
colon-separated window groups with no literal `windowN` labels.

| Flag           | Type   | Default | --params? | Description                                         |
| -------------- | ------ | ------- | --------- | --------------------------------------------------- |
| `--json`       | bool   | false   | Yes       | Output JSON: `{"status": "[0]🟣 [1]🟢"}`             |
| `--params`     | string | ""      | N/A       | Shorthand or JSON parameters                        |
| `--context-id` | string | ""      | No        | Context ID (auto-resolved from tmux session)        |
| `--session`    | string | ""      | No        | tmux session name (optional, auto-detect if in tmux) |
| `--config`     | string | ""      | No        | Path to config file                                 |

### 5.3. get-context-id

```text
tmux-a2a-postman get-context-id [--json] [--params ...] [--session NAME] [--config PATH]
```

| Flag        | Type   | Default | --params? | Description                                       |
| ----------- | ------ | ------- | --------- | ------------------------------------------------- |
| `--json`    | bool   | false   | Yes       | Output JSON: `{"context_id": "..."}`              |
| `--params`  | string | ""      | N/A       | Shorthand or JSON parameters                      |
| `--session` | string | ""      | No        | tmux session name (excluded from --params)        |
| `--config`  | string | ""      | No        | Path to config file (excluded from --params)      |

### 5.4. timeline

```text
tmux-a2a-postman timeline [--context-id ID] [--session NAME] [--config PATH] [--limit N] [--include-control-plane]
```

Always outputs pretty JSON. Reads the current journal generation only. Payloads
are recursively redacted by sensitive key name, and control-plane-only events
stay hidden unless explicitly requested.

| Flag | Type | Default | Description |
| ---- | ---- | ------- | ----------- |
| `--context-id` | string | "" | Context ID (auto-resolved from tmux session) |
| `--session` | string | "" | tmux session name (optional, auto-detect if in tmux) |
| `--config` | string | "" | Path to config file |
| `--limit` | int | 50 | Maximum number of current-generation events to print (`0` = all) |
| `--include-control-plane` | bool | false | Include control-plane-only events in the output |

### 5.5. replay

```text
tmux-a2a-postman replay [--context-id ID] [--session NAME] [--config PATH] [--surface NAME]
```

Always outputs pretty JSON. Rebuilds journal-backed projection surfaces without
mutating runtime state. Mailbox replay returns projected file paths only, not
message contents, and it does not materialize missing files on disk.

| Flag | Type | Default | Description |
| ---- | ---- | ------- | ----------- |
| `--context-id` | string | "" | Context ID (auto-resolved from tmux session) |
| `--session` | string | "" | tmux session name (optional, auto-detect if in tmux) |
| `--config` | string | "" | Path to config file |
| `--surface` | string | `all` | Projection surface: `all`, `health`, `mailbox`, `approval` |

## 6. schema Subcommand

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
| (none)                     | Curated `postman.toml` public config surface  |
| `send`                     | `send` `--params` scope                       |
| `pop`                      | `pop` `--params` scope                        |
| `read`                     | `read` `--params` scope                       |
| `todo`                     | `todo` option surface                         |
| `timeline`                 | `timeline` option surface                     |
| `replay`                   | `replay` option surface                       |
| `get-context-id`           | `get-context-id` `--params` scope             |
| `get-health-oneline`       | `get-health-oneline` `--params` scope         |
| `get-health`               | `get-health` output shape                     |

**Important:** Schema properties show only `--params`-settable flags.
Always-excluded flags (`context-id`, `config`, `session`, `file`) are
intentionally absent from schema output.

## 7. --params Flag

The `--params` flag is available on all messaging and inbox commands. It lets
callers set command options via a single argument instead of multiple flags.

### 7.1. Forms

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

### 7.2. Precedence

Explicit CLI flags override `--params` values. Use this to override a param:

```text
tmux-a2a-postman send --params 'to=worker,body=hello' --body override
# sends body="override", to="worker"
```

### 7.3. JSON Number Preservation

JSON numeric values are preserved as-is using `json.Decoder.UseNumber()`.
Large integers are not converted to scientific notation:

```text
--params '{"count":1000000}'  →  count flag gets "1000000", not "1e+06"
```

Floats are also preserved: `3.14` → `"3.14"`.

### 7.4. Always-Excluded Flags

The following flags are never settable via `--params` across all commands.
Attempting to set them returns a hard error.

| Flag           | Reason                                      |
| -------------- | ------------------------------------------- |
| `context-id`   | Security: context redirect risk             |
| `config`       | Security: config path injection             |
| `session`      | Security: session hijack risk               |
| `file`         | Security: arbitrary filesystem path         |

### 7.5. Error Messages

| Scenario                         | Error                                                    |
| -------------------------------- | -------------------------------------------------------- |
| Excluded flag in `--params`      | `--params: field "X" is not settable via --params`       |
| Non-scalar value (array)         | `--params: field "X" must be scalar, got []interface {}`           |
| Non-scalar value (object)        | `--params: field "X" must be scalar, got map[string]interface {}`  |
| Null JSON value                  | `--params: field "X" must be a scalar value, not null`   |
| Missing `=` in shorthand         | `--params: invalid shorthand pair "X": missing = separator (values containing commas require JSON form: --params '{"key":"val,with,commas"}')` |
| Invalid JSON                     | `--params JSON parse error: <decode error>`              |
| Unknown flag name                | `--params: invalid value for "X": no such flag -X`       |

### 7.6. Key Case Sensitivity

`--params` keys are matched case-sensitively against flag names. Flag names
use hyphen-lowercase form (e.g., `idempotency-key`, `no-tui`). Uppercase or
mixed-case keys will not match and produce an "unknown flag" error:

```text
--params 'To=worker'   # ERROR: no such flag -To (use "to")
--params 'to=worker'   # OK
```

### 7.7. --params Scope Discovery

To see exactly which flags are settable via `--params` for any command:

```text
tmux-a2a-postman schema send           # required: ["to","body"]
tmux-a2a-postman schema pop            # no required fields
tmux-a2a-postman schema               # postman.toml public config surface
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
| `todo summary`             | `{"nodes":[]}`          | `{"nodes":[{"node","token","state","checked","total","exists","invalid"}]}` |
| `get-context-id`           | N/A                     | `{"context_id":"..."}`                                       |
| `schema --nodes-dir`       | N/A                     | `{"xdg":"...","project_local":"..."}`                        |
| `get-health-oneline`       | N/A                     | `{"status":"[0]🟣 [1]🟢"}`                                    |
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
| `config`               | Unified state + notification model       |
| `commands`             | Command list with one-line descriptions  |
