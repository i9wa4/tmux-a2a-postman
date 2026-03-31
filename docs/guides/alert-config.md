# Alert Configuration Guide

This guide explains how the alert system is enabled in this project and how to
turn individual notification classes down or off.

## 1. Embedded Defaults vs Project Defaults

Three alert functions (`checkInboxStagnation`, `checkNodeInactivity`,
`checkUnrepliedMessages`) guard on `ui_node` being set and on per-node timeout
values being non-zero.

Embedded product defaults alone still leave `ui_node` unset and per-node alert
timeouts at zero, so alert delivery stays disabled until config enables it.

This project's shipped config surfaces now enable a low-noise profile by
default:

- repo-local sample:
  `/home/daiki.mawatari/ghq/github.com/i9wa4/tmux-a2a-postman/.tmux-a2a-postman/postman.toml`
- deployed XDG profile:
  `~/.config/tmux-a2a-postman/postman.toml`
- deployed XDG `ui_node` frontmatter:
  `~/.config/tmux-a2a-postman/postman.md`

Starting with the version that includes fix `#352`, the daemon also emits a
visible warning in the log and TUI when `ui_node` is missing or when all
per-node timeouts are zero at startup.

## 2. Current Project Default Behavior

For config loaded from `.tmux-a2a-postman/` and `~/.config/tmux-a2a-postman/`,
the current shipped defaults are:

| Mechanism | Current project-shipped default |
| --------- | ------------------------------- |
| Reminder | ON at `20` messages |
| Inbox unread summary | ON at `3+` unread via `ui_node = messenger` |
| Node inactivity alert | ON with per-node timers: `boss=3600`, `critic=1800`, `guardian=1800`, `messenger=1800`, `orchestrator=1800`, `worker=900`, `worker-alt=900` |
| Unreplied message alert | ON with the same per-node timers |
| Dropped ball detection | ON with the same per-node timers, with default delivery kept at `tui` |
| Spinning alert | OFF |
| Heartbeat | OFF |

The deployed XDG profile currently gets `ui_node: messenger` from
`postman.md` frontmatter, while the repo-local sample declares the same value
in TOML. Either surface is valid as long as the loaded config resolves
`ui_node`.

## 3. What Each Field Controls

| Field                         | Alert type affected                | Default    |
| ----------------------------- | ---------------------------------- | ---------- |
| `ui_node`                     | All alerts (required global gate)  | `""` (disabled)|
| `idle_timeout_seconds`        | Node-inactivity alert (§2.4)       | `0` (disabled)|
| `dropped_ball_timeout_seconds`| Dropped-ball detection (§2.6) and unreplied-message alert (§2.5) | `0` (disabled)|

See `docs/design/notification.md` for full details on each alert mechanism.

## 4. Opt-Out Path

You can disable or soften each notification class with ordinary config
overrides in either `~/.config/tmux-a2a-postman/postman.toml` or
`.tmux-a2a-postman/postman.toml`.

| Goal | Override |
| ---- | -------- |
| Disable reminders | `reminder_interval_messages = 0` |
| Disable inbox unread summary only | `inbox_unread_threshold = 0` |
| Disable all daemon alerts routed to the UI node | unset `ui_node` |
| Disable node inactivity alert for a node | `idle_timeout_seconds = 0` |
| Disable dropped-ball detection and unreplied-message alert for a node | `dropped_ball_timeout_seconds = 0` |
| Keep spinning alert disabled | `node_spinning_seconds = 0` |
| Keep heartbeat disabled | `[heartbeat] enabled = false` |

Repo-local config loads after XDG config with non-zero-wins semantics, so a
repo-local override will win inside this repo.

## 5. Requirement: ui_node Must Be Discoverable

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

## 6. Daemon Startup Warning

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
config values shown above.
