# bindings.toml Schema

Reference for the `bindings.toml` file used by the phony-node binding registry
(Issue #303 / Issue #308). Loaded by `internal/binding.Load()`.

## Security

The file MUST be mode `0600` (owner read/write only). `binding.Load()` rejects
files with group- or world-readable bits set (`mode & 0o044 != 0`).

## File Location

Conventional path: `$XDG_CONFIG_HOME/tmux-a2a-postman/bindings.toml`
(or wherever the operator chooses; path is passed to `Load()` directly).

## TOML Structure

The file uses TOML array-of-tables with the key `[[binding]]`. Each entry
represents one external channel / phony node association.

### Fields

| Field              | Type     | Required | Constraints                                      |
| ------------------ | -------- | -------- | ------------------------------------------------ |
| `channel_id`       | string   | yes      | `^[a-zA-Z0-9][a-zA-Z0-9-]{0,63}$`; unique       |
| `node_name`        | string   | yes      | same pattern; unique; used for routing           |
| `context_id`       | string   | yes      | same pattern; path traversal prevention          |
| `session_name`     | string   | no       | free string; empty when unassigned               |
| `pane_title`       | string   | no       | free string; empty when not used for matching    |
| `pane_node_name`   | string   | no       | same pattern as `node_name` or empty             |
| `active`           | bool     | yes      | `true` = messages delivered; `false` = dead-letter |
| `permitted_senders`| []string | yes      | each entry matches `node_name` pattern; must not be empty |

### Duplicate Constraints

- `channel_id` must be unique across all entries.
- `node_name` must be unique across all entries.

## Valid State Combinations (7-Row Table)

`binding.Load()` enforces the following state machine via `validateState()`.
Any combination not listed is rejected with an error.

| Row | active | session_name | pane_title | pane_node_name | Description                     |
| --- | ------ | ------------ | ---------- | -------------- | ------------------------------- |
| 1   | false  | (empty)      | (empty)    | (empty)        | Unassigned — registered, no pane yet |
| 2   | true   | set          | (empty)    | set            | Active, match by pane_node_name |
| 3   | true   | set          | set        | (empty)        | Active, match by pane_title     |
| 4   | true   | set          | set        | set            | Active, both matchers           |
| 5   | false  | set          | (empty)    | set            | Inactive, was node_name-matched |
| 6   | false  | set          | set        | (empty)        | Inactive, was title-matched     |
| 7   | false  | set          | set        | set            | Inactive, was both-matched      |

Row 1 is the initial state after Phase A registration (§6.1 Phase A).
Rows 2–4 are set by Phase B assignment (§6.1 Phase B).
Rows 5–7 are set by teardown (§6.2).

## Example

```toml
# bindings.toml — mode 0600 required
# Managed by: tmux-a2a-postman bind register/assign/deactivate/rebind

# Row 1: unassigned (Phase A complete, Phase B pending)
[[binding]]
channel_id        = "slack-general"
node_name         = "slack-bot"
context_id        = "session-20260101-120000-abcd"
session_name      = ""
pane_title        = ""
pane_node_name    = ""
active            = false
permitted_senders = ["orchestrator", "worker"]

# Row 3: active, matched by pane_title (Phase B complete)
[[binding]]
channel_id        = "gh-notifications"
node_name         = "gh-monitor"
context_id        = "session-20260101-120000-abcd"
session_name      = "my-tmux-session"
pane_title        = "gh-monitor-pane"
pane_node_name    = ""
active            = true
permitted_senders = ["orchestrator"]
```

## Save() Limitation

`binding.Save()` serialises the registry back to TOML using the
`github.com/BurntSushi/toml` encoder. TOML comments in the original file are
NOT preserved — the written file will contain only the structured data.
Operators should treat comments as advisory and regenerate them from this
schema document after any `Save()` call.
