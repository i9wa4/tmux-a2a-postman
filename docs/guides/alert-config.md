# Alert Configuration Guide

This guide explains how to configure the daemon's stale-node alert system.
Without the settings below, all alert paths are silently disabled.

## 1. Why Alerts Require Explicit Config

Three alert functions (`checkInboxStagnation`, `checkNodeInactivity`,
`checkUnrepliedMessages`) guard on `ui_node` being set and on per-node
timeout values being non-zero. A daemon that starts without these settings
produces no alerts, regardless of node activity.

Starting with the version that includes this fix (#352), the daemon emits a
visible warning in the log and TUI when either condition is detected at startup.

## 2. Minimal Working Config

### 2.1. postman.toml

```toml
[postman]
ui_node = "messenger"

[nodes.worker]
idle_timeout_seconds       = 900
dropped_ball_timeout_seconds = 900

[nodes.orchestrator]
idle_timeout_seconds       = 900
dropped_ball_timeout_seconds = 900
```

### 2.2. postman.md (alternative / additional)

```markdown
---
ui_node: messenger
---
```

## 3. What Each Field Controls

| Field                         | Alert type affected                | Default    |
| ----------------------------- | ---------------------------------- | ---------- |
| `ui_node`                     | All alerts (required global gate)  | `""` (disabled)|
| `idle_timeout_seconds`        | Node-inactivity alert (§2.4)       | `0` (disabled)|
| `dropped_ball_timeout_seconds`| Unreplied-message alert (§2.5)     | `0` (disabled)|

See `docs/design/notification.md` for full details on each alert mechanism.

## 4. Requirement: ui_node Must Be Discoverable

The `ui_node` value must match a tmux pane title in the active session.
If the pane is absent, alert messages route to `dead-letter/` silently.

Set the pane title before starting the daemon:

```sh
tmux select-pane -T messenger
```

Verify the pane is discovered:

```sh
tmux-a2a-postman get-session-health
```

The `messenger` node must appear with `inbox_count` and `waiting_count` in the
output.

## 5. Daemon Startup Warning

If the daemon detects a misconfigured alert system it logs:

```text
postman: WARNING: alert system disabled: ui_node is not set. ...
```

or:

```text
postman: WARNING: alert system partially disabled: no nodes have
    idle_timeout_seconds or dropped_ball_timeout_seconds set. ...
```

These warnings also appear in the TUI event log. Resolve them by adding the
config values shown in Section 2.
