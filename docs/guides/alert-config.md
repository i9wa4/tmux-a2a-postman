# Alert Configuration Guide

This guide explains how the alert system is enabled in this project and how to
turn individual notification classes down or off.

## 1. Embedded Defaults vs Optional Local Profiles

Three alert functions (`checkInboxStagnation`, `checkNodeInactivity`,
`checkUnrepliedMessages`) guard on `ui_node` being set and on per-node timeout
values being non-zero. The waiting-state ticker separately emits a stalled alert
when a reply-tracked wait crosses into `stalled`.

Embedded product defaults in `internal/config/postman.default.toml` now set
`ui_node = "messenger"` and ship a non-empty `stalled_alert_template`, so
reply-tracked `composing -> stalled` and `spinning -> stalled` transitions
notify the human-facing node by default.

Embedded product defaults still keep all per-node `idle_timeout_seconds` and
`dropped_ball_timeout_seconds` at `0`, and keep `node_spinning_seconds = 0`, so
node-inactivity, unreplied-message, dropped-ball, and spinning alerts remain
off until local or XDG config enables them.

Local `.tmux-a2a-postman/` and `~/.config/tmux-a2a-postman/` config can still
layer a fuller operator profile on top, but that is separate from the embedded
file and should be explained as an environment-specific choice.

`message_footer` controls reply guidance only for stored messages written by
`send`. TOML config and XDG `postman.md` can replace that footer; project-local
`postman.md` appends its `message_footer` to the effective base footer. Daemon
alerts and heartbeat mail use `daemon_message_template`, and dead-letter
notifications embed their own re-send instructions. `pop` should print the
delivered message body as stored, not invent a second hard-coded reply hint.

Starting with the versions that include fixes `#352` and `#383`, the daemon
emits explicit alert-delivery degraded or recovered signals in the daemon log.
Degraded is emitted when the effective `ui_node` is not discoverable in the
current session or when all per-node timeouts are zero. Recovered is emitted
after a degraded state clears because `ui_node` becomes discoverable again and
per-node alert thresholds are active.

## 2. Current Embedded Default Behavior

Without any local or XDG override, the embedded defaults behave like this:

| Mechanism | Current embedded default |
| --------- | ------------------------ |
| Reminder | ON at `20` messages |
| Inbox unread summary | ON at `3+` unread via `ui_node = messenger` |
| Reply-tracked stalled alert | ON when a reply-tracked wait crosses into `stalled`, routed to `messenger` |
| Node inactivity alert | OFF (all `idle_timeout_seconds = 0`) |
| Unreplied message alert | OFF (all `dropped_ball_timeout_seconds = 0`) |
| Dropped ball detection | OFF (all `dropped_ball_timeout_seconds = 0`) |
| Expected-reply overdue alert (`spinning`) | OFF (`node_spinning_seconds = 0`) |
| Heartbeat | OFF |

If your local or XDG profile also defines per-node timeouts, that environment
can enable node-inactivity, unreplied-message, and dropped-ball alerts even
though the embedded file keeps them off.

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
| A daemon-generated inbox message to `ui_node` | Policy alert routed to the human-facing node | `ui_node`, `inbox_unread_threshold`, `idle_timeout_seconds`, `dropped_ball_timeout_seconds`, `node_spinning_seconds`, `stalled_alert_template` |
| A dropped-ball message in the TUI or status bar | Coordination signal separate from `ui_node` inbox alerts | `dropped_ball_timeout_seconds`, `dropped_ball_notification` |
| Visible `spinning` with no inbox alert | Reply-tracked wait crossed the display threshold, but the alert path is disabled or suppressed | `node_spinning_seconds`, `ui_node`, `alert_cooldown_seconds`, `alert_delivery_window_seconds` |
| Visible `stalled` with no inbox alert | Reply-tracked wait went stale, but the stalled alert path is disabled or suppressed | `ui_node`, `stalled_alert_template`, `alert_cooldown_seconds`, `alert_delivery_window_seconds` |

## 4. What Each Field Controls

| Field                           | Surface / mechanism                                 | Default              |
| ------------------------------- | --------------------------------------------------- | -------------------- |
| `ui_node`                       | Global gate for daemon alerts and `user_input` waits | `"messenger"`       |
| `reminder_interval_messages`    | Pane reminder cadence after archived reads          | `20`                 |
| `inbox_unread_threshold`        | `ui_node` unread-summary alert                      | `3`                  |
| `idle_timeout_seconds`          | Per-node inactivity alert threshold                 | `0` (disabled)       |
| `dropped_ball_timeout_seconds`  | Per-node unreplied-message and dropped-ball threshold | `0` (disabled)     |
| `node_spinning_seconds`         | Reply-tracked `composing -> spinning` threshold     | `0` (disabled)       |
| `stalled_alert_template`        | Reply-tracked `... -> stalled` alert body           | (see config)         |
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
| Disable all daemon alerts routed to the UI node | `ui_node = ""` |
| Disable node inactivity alert for a node | `idle_timeout_seconds = 0` |
| Disable dropped-ball detection and unreplied-message alert for a node | `dropped_ball_timeout_seconds = 0` |
| Disable expected-reply overdue alert | `node_spinning_seconds = 0` |
| Keep heartbeat disabled | `[heartbeat] enabled = false` |

Repo-local config loads after XDG config with non-zero-wins semantics, so a
repo-local override will win inside this repo. `ui_node = ""` is treated as an
explicit override, so a project-local TOML or Markdown file can clear the
embedded `messenger` default.

## 6. Requirement: ui_node Must Be Discoverable

The effective `ui_node` value must match a tmux pane title in the active
session. With the embedded defaults, that effective value is `messenger`
unless local or XDG config overrides it.

If the pane is absent, alert messages can route to `dead-letter/` until that
pane appears. The daemon now warns at startup when the effective `ui_node` is
not discoverable in the current session.

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

## 7. Daemon Alert-Delivery Signals

If the daemon detects a degraded alert-delivery path it logs:

```text
postman: WARNING: alert delivery degraded: ui_node "messenger" is not discoverable in this session. ...
```

or:

```text
postman: WARNING: alert delivery degraded: no nodes have
    idle_timeout_seconds or dropped_ball_timeout_seconds set. ...
```

When those conditions clear, the daemon logs:

```text
postman: INFO: alert delivery recovered: ui_node "messenger" is discoverable and per-node alert thresholds are active.
```

Resolve degraded signals by adding the config values shown above or by making
the `ui_node` pane discoverable again. Use the daemon log as the reliable
startup signal; the reduced default TUI does not expose a separate event-log
pane. Treat the same daemon log as the runtime source for degraded and
recovered transitions after startup.
