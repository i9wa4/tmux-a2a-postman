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
visible startup warning in the daemon log when `ui_node` is missing or when
all per-node timeouts are zero at startup.

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
| Expected-reply overdue alert (`spinning`) | OFF (`node_spinning_seconds = 0`) |
| Heartbeat | OFF |

The deployed XDG profile currently gets `ui_node: messenger` from
`postman.md` frontmatter, while the repo-local sample declares the same value
in TOML. Either surface is valid as long as the loaded config resolves
`ui_node`.

## 3. Unified State + Notification Boundary

`get-health`, `get-health-oneline`, and the default TUI share one canonical
health contract. This guide covers the policy knobs layered on top of that
contract:

- state/view authority: canonical `visible_state`, per-node `pane_state`, and
  `waiting_state`
- policy knobs: `ui_node`, reminder thresholds, per-node inactivity and
  late-reply thresholds, `node_spinning_seconds`, and heartbeat enablement
- dampening knobs: `alert_cooldown_seconds`,
  `alert_delivery_window_seconds`, `pane_notify_cooldown_seconds`, and
  `dropped_ball_cooldown_seconds`

The current visible states are `ready`, `pending`, `user_input`, `composing`,
`spinning`, and `stalled`. Session-level `unavailable` is a fallback for
non-authoritative health views, not a per-node alert state.

### 3.1. Operator Triage Map

Use this quick split before changing config:

| If you see | Read it as | First knob / doc to check |
| ---------- | ---------- | ------------------------- |
| `pending`, `user_input`, `composing`, `spinning`, `stalled` in `get-health`, `get-health-oneline`, or the TUI | Canonical visible state, not a daemon alert by itself | `docs/design/node-state-machine.md` |
| A pane hint telling the recipient to run `tmux-a2a-postman pop` | Delivery-side notification for that recipient pane | `notification_template`, `pane_notify_cooldown_seconds`, `docs/design/notification.md` |
| A daemon-generated inbox message to `ui_node` | Policy alert routed to the human-facing node | `ui_node`, `inbox_unread_threshold`, `idle_timeout_seconds`, `dropped_ball_timeout_seconds`, `node_spinning_seconds` |
| A dropped-ball message in the TUI or status bar | Coordination signal separate from `ui_node` inbox alerts | `dropped_ball_timeout_seconds`, `dropped_ball_notification` |
| Visible `spinning` with no inbox alert | Reply-tracked wait crossed the display threshold, but the alert path is disabled or suppressed | `node_spinning_seconds`, `ui_node`, `alert_cooldown_seconds`, `alert_delivery_window_seconds` |

## 4. What Each Field Controls

| Field                           | Surface / mechanism                                 | Default              |
| ------------------------------- | --------------------------------------------------- | -------------------- |
| `ui_node`                       | Global gate for daemon alerts and `user_input` waits | `""` (disabled)     |
| `reminder_interval_messages`    | Pane reminder cadence after archived reads          | `20`                 |
| `inbox_unread_threshold`        | `ui_node` unread-summary alert                      | `3`                  |
| `idle_timeout_seconds`          | Per-node inactivity alert threshold                 | `0` (disabled)       |
| `dropped_ball_timeout_seconds`  | Per-node unreplied-message and dropped-ball threshold | `0` (disabled)     |
| `node_spinning_seconds`         | Reply-tracked `composing -> spinning` threshold     | `0` (disabled)       |
| `alert_cooldown_seconds`        | Shared alert rate limiter for `ui_node`             | `600`                |
| `alert_delivery_window_seconds` | Recent-delivery suppression for `ui_node`           | `60`                 |
| `pane_notify_cooldown_seconds`  | Reminder / pane-send cooldown                       | `600`                |
| `dropped_ball_cooldown_seconds` | Per-node dropped-ball resend dampener               | `0` -> timeout value |
| `dropped_ball_notification`     | Dropped-ball delivery channel (`tui`, `display`, `all`) | `"tui"`         |
| `[heartbeat].enabled`           | Turns heartbeat automation on or off                | `false`              |
| `[heartbeat].interval_seconds`  | Heartbeat cadence                                   | `1800`               |

See `docs/design/notification.md` for full details on each alert mechanism.

## 5. Opt-Out Path

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
| Disable expected-reply overdue alert | `node_spinning_seconds = 0` |
| Keep heartbeat disabled | `[heartbeat] enabled = false` |

Repo-local config loads after XDG config with non-zero-wins semantics, so a
repo-local override will win inside this repo.

## 6. Requirement: ui_node Must Be Discoverable

The `ui_node` value must match a tmux pane title in the active session.
If the pane is absent, alert messages route to `dead-letter/` silently.

Set the pane title before starting the daemon:

```sh
tmux select-pane -T messenger
```

Verify the pane is discovered:

```sh
tmux-a2a-postman get-health
```

The `messenger` node must appear in `nodes[*]`, with the canonical
`visible_state` plus the live `inbox_count` and `waiting_count` facts.

## 7. Daemon Startup Warning

If the daemon detects a misconfigured alert system it logs:

```text
postman: WARNING: alert system disabled: ui_node is not set. ...
```

or:

```text
postman: WARNING: alert system partially disabled: no nodes have
    idle_timeout_seconds or dropped_ball_timeout_seconds set. ...
```

Resolve these warnings by adding the config values shown above. Use the daemon
log as the reliable startup signal; the reduced default TUI does not expose a
separate event-log pane.
