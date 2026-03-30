# Notification System Design

## 1. Overview

tmux-a2a-postman delivers messages between AI agents running in tmux panes. When
a message arrives or a problem is detected, the daemon "taps the agent on the
shoulder" — this is a notification. There are seven distinct notification
mechanisms, each designed for a different situation.

This document explains all seven mechanisms: when each fires, how it reaches its
target, which configuration fields control it, and how the guard/throttle system
prevents notification floods.

### 1.1. Glossary

| Term                  | Definition                                                               |
| --------------------- | ------------------------------------------------------------------------ |
| pane notification     | Text injected directly into a tmux pane buffer via `set-buffer` + `paste-buffer` + `C-m` |
| alert                 | A daemon-generated message written to `post/` and routed to `ui_node`   |
| reminder              | A pane notification sent after N messages are archived without reply     |
| heartbeat             | A periodic message written to `post/` for `llm_node` (keeps it active)  |
| PING                  | A daemon-originated liveness-check message sent for discovery, restart, or manual recheck |
| ui_node               | Node designated to receive all alert messages (configured globally)      |
| llm_node              | Node designated to receive heartbeat messages (configured in `[heartbeat]`) |
| post/                 | Outgoing message staging directory; daemon watches and delivers from here |
| inbox/{node}/         | Incoming messages for a node; arrival here triggers pane notification    |
| read/                 | Archive of messages the recipient has read                               |
| dead-letter/          | Directory for messages that could not be delivered                       |
| waiting/              | Directory for waiting-file state tracking (composing/spinning/stuck)     |
| cooldown              | Minimum interval enforced between two notifications of the same type     |
| dropped ball          | A node received a message but has not sent any message since             |
| contextId             | Unique session identifier shared by all nodes in one daemon invocation   |

---

## 2. Notification Mechanisms

### 2.1. Summary Table

| # | Mechanism               | Trigger                                        | Delivery             | Target     |
| - | ----------------------- | ---------------------------------------------- | -------------------- | ---------- |
| 1 | Pane notification       | Message arrives in `inbox/{node}/`             | `SendToPane`         | Recipient pane |
| 2 | Reminder                | N messages archived to `read/` without reply   | `SendToPane`         | Recipient pane |
| 3 | Inbox unread summary    | Unread count >= threshold (30 s tick)          | `post/` routing      | `ui_node`  |
| 4 | Node inactivity alert   | Node idle > timeout (30 s tick)                | `post/` routing      | `ui_node`  |
| 5 | Unreplied message alert | Message in `read/` > timeout (30 s tick)       | `post/` routing      | `ui_node`  |
| 6 | Dropped ball detection  | `LastReceived > LastSent` > timeout            | TUI event + optional `tmux display-message` | TUI / status bar |
| 7 | Heartbeat trigger       | Periodic interval                              | Write to `post/`     | `llm_node` |

### 2.1.1. Message Classification (Current Tree)

| Traffic                       | Primary Surface      | Control-Plane Role            | Current Overlap / Note                                                                 |
| ----------------------------- | -------------------- | ----------------------------- | -------------------------------------------------------------------------------------- |
| Pane notification             | pane-only            | operator-visible              | Immediate companion to inbox delivery; current default text shows the delivered filename and tells the node to run `tmux-a2a-postman pop` |
| Reminder                      | pane-only            | operator-visible              | No inbox message is created; per-pane cooldown can suppress the reminder quietly      |
| Inbox unread summary alert    | inbox-visible        | operator-visible              | Routed to `ui_node` inbox and therefore also causes the usual pane notification on delivery |
| Node inactivity alert         | inbox-visible        | operator-visible              | Same `ui_node` inbox + pane overlap as other alert envelopes                          |
| Unreplied message alert       | inbox-visible        | operator-visible              | Same `ui_node` inbox + pane overlap as other alert envelopes                          |
| Spinning alert                | inbox-visible        | operator-visible              | Produced by the waiting-file state machine and overlaps with the yellow `spinning` TUI/oneline state |
| Dropped ball detection        | TUI-only             | operator-visible              | Default `dropped_ball_notification = "tui"` keeps it off the inbox path; `display` / `all` adds a tmux status-bar side channel |
| PING                          | inbox-visible        | control-plane + operator-visible | The current tree uses PING beyond startup: first discovery, pane restarts, and manual TUI rechecks still surface as readable daemon mail |
| Heartbeat trigger             | inbox-visible        | control-plane + operator-visible | Written to `post/` for `llm_node`, then routed like normal mail; single-slot guard keeps it from flooding |

Current-tree note: none of the named daemon-generated traffic is hidden
control-plane only. PING and heartbeat are control-plane by purpose, but both
remain operator-visible because they land as daemon mail and inherit the usual
delivery surfaces.

---

### 2.1. Pane Notification

**What it does:** When a message is successfully delivered to a node's
`inbox/{node}/` directory, the daemon immediately injects a notification hint
into that node's tmux pane. The hint is rendered from `notification_template`,
greets the node, shows the delivered message filename, and prompts the node to
run `pop`.

**When it fires:** On every successful message delivery. The per-pane cooldown
is bypassed (`bypassCooldown=true`) so every incoming message triggers a
notification regardless of how recently the pane was last notified.

**Delivery:** `notification.SendToPane` — wraps the hint in protocol sentinels
(`<!-- message start -->` / `<!-- end of message -->`), calls `tmux set-buffer`
→ `tmux paste-buffer -t {paneID}` → `tmux send-keys -t {paneID} C-m`.

**Config fields:**

| Field                        | Default | Description                                      |
| ---------------------------- | ------- | ------------------------------------------------ |
| `notification_template`      | `"Hello, {node}!\nYou've got mail: {filename}\nRun tmux-a2a-postman pop to read it."` | Template for the pane hint text |
| `enter_delay_seconds`        | `3.0`   | Delay before sending `C-m`                       |
| `pane_notify_cooldown_seconds` | `600` | Cooldown for reminder/alert pane sends (NOT applied here; bypassed for direct delivery) |

**Source:** `internal/notification/notification.go` — `BuildNotification`,
`SendToPane`

**Template variables:** `{from_node}`, `{node}`, `{timestamp}`, `{filename}`
(used by the current default as the visible message identifier),
`{inbox_path}`, `{talks_to_line}`, `{template}`, `{reply_command}`,
`{context_id}`

---

### 2.2. Reminder

**What it does:** Counts how many messages a node has had archived to `read/`
without replying. When the count reaches `reminder_interval_messages`, the
daemon injects a reminder hint directly into the node's pane (same
`SendToPane` delivery as above) and resets the counter.

**When it fires:** Each time a message is moved from `inbox/` to `read/`, the
counter increments. When `counter >= reminder_interval_messages`, a reminder is
sent and the counter resets to zero. A cumulative (never-resetting) counter is
also tracked for TUI display (Issue #246).

**Delivery:** `notification.SendToPane` with `bypassCooldown=false` — the
per-pane cooldown (`pane_notify_cooldown_seconds`) applies, so if the pane was
recently notified the reminder is silently skipped.

**Config fields:**

| Field                        | Default         | Scope          |
| ---------------------------- | --------------- | -------------- |
| `reminder_interval_messages` | `20`            | Global         |
| `reminder_message`           | `"{inbox_path}"` | Global        |
| `pane_notify_cooldown_seconds` | `600`         | Global         |

Node-level overrides (under `[nodes.{name}]`):

| Field                        | Description                           |
| ---------------------------- | ------------------------------------- |
| `reminder_interval_messages` | Override global interval for this node |
| `reminder_message`           | Override global reminder text          |

**Source:** `internal/reminder/reminder.go` — `ReminderState.Increment`

**Template variables for `reminder_message`:** `{node}`, `{count}`,
`{template}`, `{inbox_path}`

---

### 2.3. Inbox Unread Summary Alert

**What it does:** Periodically counts unread messages in each node's
`inbox/{node}/` directory. When the count reaches or exceeds
`inbox_unread_threshold`, an alert message is sent to `ui_node` via normal
`post/` routing (the daemon routes and pane-notifies `ui_node` as usual).

**When it fires:** Every 30 seconds (`inboxCheckTicker`). Three guards must all
pass:

- Guard 1: `alertRateLimiter.Allow(ui_node)` — `alert_cooldown_seconds` has
  elapsed since the last alert to `ui_node`
- Guard 2: `alert_delivery_window_seconds` has elapsed since `ui_node` last
  received any regular message
- Guard 3: The unread count has increased since the last alerted count
  (Issue 264 — prevents re-sending the same count)

**Delivery:** `sendAlertToUINode` writes an envelope to `post/`, which the
daemon then routes to `ui_node`'s inbox and pane-notifies normally. The alert
body is rendered from `alert_message_template` and
`inbox_unread_summary_alert_template`.

**Config fields:**

| Field                              | Default | Description                                     |
| ---------------------------------- | ------- | ----------------------------------------------- |
| `ui_node`                          | `""`    | Recipient node for all alerts (required)         |
| `inbox_unread_threshold`           | `3`     | Minimum unread count to trigger alert            |
| `alert_cooldown_seconds`           | `600`   | Min interval between alerts to same recipient    |
| `alert_delivery_window_seconds`    | `60`    | Suppress if `ui_node` received recently          |
| `inbox_unread_summary_alert_template` | (see default config) | Alert body text               |
| `alert_message_template`           | (see default config) | Envelope wrapping the alert body |
| `alert_action_reachable_template`  | (see default config) | Appended when node is reachable |
| `alert_action_unreachable_template` | (see default config) | Appended when node is not reachable |

**Source:** `internal/daemon/daemon.go` — `checkInboxStagnation`

**Template variables for `inbox_unread_summary_alert_template`:** `{node}`,
`{count}`, `{threshold}`

---

### 2.4. Node Inactivity Alert

**What it does:** Monitors each node's activity timestamps (last sent, last
received). When a node has been idle (no sends and no receives) for longer than
its `idle_timeout_seconds`, an alert is sent to `ui_node`.

**When it fires:** Every 30 seconds. Three guards must all pass:

- Guard 1: `alertRateLimiter.Allow(ui_node)`
- Guard 2: `alert_delivery_window_seconds` since `ui_node` last received
- Guard 3 (signal filter): Nodes with a `state: user_input` waiting file are
  excluded (the silence is intentional — a human is being prompted)

**Delivery:** Same as 2.3 — `sendAlertToUINode` → `post/` routing.

**Config fields:**

| Field                           | Default | Scope   |
| ------------------------------- | ------- | ------- |
| `ui_node`                       | `""`    | Global  |
| `alert_cooldown_seconds`        | `600`   | Global  |
| `alert_delivery_window_seconds` | `60`    | Global  |
| `node_inactivity_alert_template` | (see default config) | Global |
| `idle_timeout_seconds`          | (none)  | Per-node |

See `docs/guides/alert-config.md` for the minimal working config required to
activate this alert.

**Source:** `internal/daemon/daemon.go` — `checkNodeInactivity`

**Template variables for `node_inactivity_alert_template`:** `{node}`,
`{severity}`, `{inactive_duration}`, `{threshold}`, `{last_sent}`,
`{last_received}`, `{liveness_confirmed}`

---

### 2.5. Unreplied Message Alert

**What it does:** Scans each node's `read/` directory for messages that are
older than `dropped_ball_timeout_seconds` without a reply being sent. When such
messages exist, an alert is sent to `ui_node`. Daemon-generated messages (sender
`postman`) are excluded. Already-alerted file paths are suppressed to prevent
repeat alerts for the same message.

**When it fires:** Every 30 seconds. Three guards:

- Guard 1: `alertRateLimiter.Allow(ui_node)`
- Guard 2: `alert_delivery_window_seconds` since `ui_node` last received
- Guard 3: Files already in `alertedReadFiles` set are suppressed

**Delivery:** `sendAlertToUINode` → `post/` routing.

**Config fields:**

| Field                           | Default | Scope    |
| ------------------------------- | ------- | -------- |
| `ui_node`                       | `""`    | Global   |
| `alert_cooldown_seconds`        | `600`   | Global   |
| `alert_delivery_window_seconds` | `60`    | Global   |
| `unreplied_message_alert_template` | (see default config) | Global |
| `dropped_ball_timeout_seconds`  | `0` (disabled) | Per-node |

See `docs/guides/alert-config.md` for the minimal working config required to
activate this alert.

**Source:** `internal/daemon/daemon.go` — `checkUnrepliedMessages`

**Template variables for `unreplied_message_alert_template`:** `{node}`,
`{count}`, `{time_since_read}`, `{from}`, `{threshold}`

---

### 2.6. Dropped Ball Detection

**What it does:** Detects nodes that received a message but have not sent any
message since (the "ball" has been dropped). Uses `IdleTracker.IsHoldingBall`
which compares `LastReceived > LastSent`. When the hold duration exceeds
`dropped_ball_timeout_seconds` and the cooldown has elapsed, the daemon emits a
TUI event and optionally sends a `tmux display-message` to the status bar.

**When it fires:** Checked via `IdleTracker.CheckDroppedBalls` during the daemon
event loop. Prerequisites:

- Node liveness must be confirmed (PING replied)
- `dropped_ball_timeout_seconds > 0` for the node
- Hold duration > `dropped_ball_timeout_seconds`
- `dropped_ball_cooldown_seconds` has elapsed since last notification

**Delivery:** Two channels (controlled by `dropped_ball_notification`):

- `"tui"` (default): emits a `dropped_ball` event to the TUI events channel
- `"display"`: calls `tmux display-message {eventMessage}` (appears in tmux
  status bar)
- `"all"`: both TUI event and `tmux display-message`

**Config fields (per-node under `[nodes.{name}]`):**

| Field                           | Default | Description                                     |
| ------------------------------- | ------- | ----------------------------------------------- |
| `dropped_ball_timeout_seconds`  | `0`     | Must be > 0 to enable detection for this node   |
| `dropped_ball_cooldown_seconds` | `0` → defaults to `dropped_ball_timeout_seconds` | Min interval between notifications |
| `dropped_ball_notification`     | `"tui"` | Delivery channel: `"tui"`, `"display"`, `"all"` |

Global:

| Field                        | Default | Description                     |
| ---------------------------- | ------- | ------------------------------- |
| `dropped_ball_event_template` | (see default config) | Event message template |

**Source:** `internal/idle/idle.go` — `IdleTracker.CheckDroppedBalls`,
`IdleTracker.IsHoldingBall`, `IdleTracker.MarkDroppedBallNotified`; delivery
logic in `internal/daemon/daemon.go`

**Important limitation:** `IsHoldingBall` uses a simple
`LastReceived > LastSent` heuristic. In multi-sender scenarios this may produce
false positives. A note to this effect is in the source code (Issue #56).

---

### 2.7. Heartbeat Trigger

**What it does:** At a configured interval, the daemon writes a heartbeat
message to `post/` addressed to `llm_node`. The message is routed and delivered
normally, prompting the LLM node to respond. Single-slot semantics: if
`llm_node`'s inbox is non-empty, the trigger is skipped; stale triggers older
than `2 * interval` are recycled to `dead-letter/`.

**When it fires:** Every `interval_seconds`. Fires only when `llm_node`'s inbox
is empty (prevents flooding an unresponsive LLM). Requires
`heartbeat_message_template` to be set; otherwise the trigger is a no-op.

**Delivery:** `os.WriteFile` to `post/` — then normal daemon routing delivers it
to `llm_node`'s inbox and sends a pane notification.

**Config fields (under `[heartbeat]`):**

| Field                       | Default | Description                                      |
| --------------------------- | ------- | ------------------------------------------------ |
| `llm_node`                  | `""`    | Target node for heartbeat messages               |
| `interval_seconds`          | (none)  | Interval between triggers                         |
| `prompt`                    | (none)  | Prompt text (supports `{context_id}`)            |
| `heartbeat_message_template` | (see default config) | Envelope template for heartbeat messages |

**Source:** `internal/heartbeat/trigger.go` — `SendHeartbeatTrigger`

---

## 3. Supporting Systems

### 3.1. Waiting File State Machine

When a message is sent to a node, a waiting file is created in `waiting/` to
track whether the node is composing a reply. The file transitions through
states:

```text
composing ──(idle threshold elapsed, pane active, spinning enabled)──> spinning
composing ──(idle threshold elapsed, pane stale)──────────────────────> stuck
spinning  ──(pane stale)───────────────────────────────────────────────> stuck
```

States:

- `composing` — Message sent; awaiting reply within idle window
- `spinning` — Composing window elapsed but pane is still active (possible loop)
- `stuck` — Pane went stale; agent appears unresponsive
- `user_input` — Message sent to `ui_node`; human is being prompted

When `spinning` is detected, a `spinning_alert_template` alert is sent to
`ui_node`. The `node_inactivity` alert suppresses nodes with `state: user_input`
files (Guard 3).

**Config fields:**

| Field                     | Default | Description                                     |
| ------------------------- | ------- | ----------------------------------------------- |
| `node_idle_seconds`       | (none)  | Composing window before transition               |
| `node_spinning_seconds`   | `0`     | Spinning threshold (0 = disabled)                |
| `spinning_alert_template` | (see default config) | Alert body for spinning state      |

**Source:** `internal/daemon/daemon.go` (state transition logic in ticker loop)

### 3.2. Dead-Letter Notifications

When a message cannot be delivered (routing violation, missing node, etc.), the
daemon moves the file to `dead-letter/` and writes a notification directly to
the sender's inbox, bypassing `post/` routing. The edge violation warning uses
`edge_violation_warning_template`.

**Source:** `internal/daemon/daemon.go`

### 3.3. Liveness Tracking (PING / Liveness Confirmed)

The daemon sends PING as a direct system message when it needs to confirm
liveness. In the current tree that includes first discovery, pane-restart
rechecks, and manual TUI-triggered PING. When `ui_node` is configured, a given
PING pass is restricted to that node; otherwise it is sent to all discovered
nodes in the target session (`main.go` — `// If ui_node is configured, restrict
PING to that node only.`).

Liveness is confirmed via two independent paths:

1. **PING reply**: when a node archives the PING (moves it from inbox to
   `read/`), `IdleTracker.MarkNodeAlive` is called, setting
   `LivenessConfirmed = true` for that node.
2. **`read/` move event**: whenever a node archives any message (inbox →
   `read/`), `MarkNodeAlive` is called directly — independent of PING. A node
   that never received a PING can still have liveness confirmed the first time
   it archives any message.

Dropped ball detection (§2.6) only fires for nodes with confirmed liveness —
prevents false alerts for nodes that have never been active.

**Source:** `internal/ping/ping.go` — `SendPingToNode`; `internal/idle/idle.go`
— `MarkNodeAlive`; `internal/daemon/daemon.go:551-564` — `read/` move liveness
confirmation (Issue #150)

### 3.4. Pane Activity Tracking (Hybrid Idle Detection)

`IdleTracker.StartPaneCaptureCheck` periodically captures pane content via
`tmux capture-pane`. Two consecutive content changes within
`activity_window_seconds` mark the pane as "active". This feeds into the waiting
file state machine (composing → spinning transition requires
`paneState == "active"`).

Pane states: `active` (recent change), `idle` (within `node_idle_seconds`),
`stale` (beyond `node_stale_seconds`).

**Source:** `internal/idle/idle.go` — `StartPaneCaptureCheck`,
`checkPaneCapture`

---

## 4. Delivery Methods

| Method               | Used by                                    | How                                           |
| -------------------- | ------------------------------------------ | --------------------------------------------- |
| `SendToPane`         | Pane notification (§2.1), Reminder (§2.2)  | `tmux set-buffer` + `paste-buffer` + `C-m`    |
| `post/` routing      | Inbox unread (§2.3), Node inactivity (§2.4), Unreplied (§2.5), Heartbeat (§2.7) | Write to `post/`; daemon routes to inbox + `SendToPane` |
| Direct inbox write   | Dead-letter notifications (§3.2)           | `os.WriteFile` directly to sender's inbox; bypasses `post/` |
| `tmux display-message` | Dropped ball (§2.6, when `"display"` or `"all"`) | `tmux display-message {text}`            |
| TUI events channel   | Dropped ball (§2.6), Inbox unread (§2.3), Node inactivity (§2.4), Unreplied (§2.5) | Internal Go channel; rendered in TUI overlay |

---

## 5. Guard / Throttle Mechanisms

### 5.1. Per-Pane Cooldown

Applies to: **Reminder** (§2.2) only. Pane notifications from direct delivery
(§2.1) always bypass this.

- `pane_notify_cooldown_seconds` (global, default 600 s)
- Tracked in `notification.paneLastNotified` map (keyed by pane ID)
- `SendToPane` with `bypassCooldown=false` skips silently if within cooldown

### 5.2. Alert Rate Limiter

Applies to: **Inbox unread** (§2.3), **Node inactivity** (§2.4),
**Unreplied message** (§2.5).

- `alert_cooldown_seconds` (global, default 600 s)
- Tracked in `alert.AlertRateLimiter` (keyed by recipient node name)
- Keyed by recipient only — any alert type to a node resets the cooldown for
  that node

### 5.3. Alert Delivery Window

Applies to: **Inbox unread** (§2.3), **Node inactivity** (§2.4),
**Unreplied message** (§2.5).

- `alert_delivery_window_seconds` (global, default 60 s)
- Suppresses alert if `ui_node` received any regular message within this window
- Checked via `idleTracker.GetLastReceived(ui_node)` before sending

### 5.4. Dropped Ball Cooldown

Applies to: **Dropped ball detection** (§2.6) only.

- Per-node `dropped_ball_cooldown_seconds` (default: same as
  `dropped_ball_timeout_seconds`)
- Tracked in `NodeActivity.LastNotifiedDropped`

### 5.5. Heartbeat Single-Slot Guard

Applies to: **Heartbeat trigger** (§2.7) only.

- Trigger is skipped if `llm_node`'s inbox has any unread messages
- Stale triggers (age > `2 * interval_seconds`) are recycled to `dead-letter/`

### 5.6. Guard Interaction Summary

```text
Mechanism                 | Per-pane cooldown | AlertRateLimiter | Delivery window | Node-specific cooldown
------------------------- | ----------------- | ---------------- | --------------- | ---------------------
Pane notification (§2.1)  | bypassed          | —                | —               | —
Reminder (§2.2)           | applied           | —                | —               | —
Inbox unread (§2.3)       | —                 | Guard 1          | Guard 2         | —
Node inactivity (§2.4)    | —                 | Guard 1          | Guard 2         | —
Unreplied message (§2.5)  | —                 | Guard 1          | Guard 2         | —
Dropped ball (§2.6)       | —                 | —                | —               | dropped_ball_cooldown
Heartbeat (§2.7)          | —                 | —                | —               | single-slot (inbox)
```

---

## 6. Config Reference

All notification-related fields from `internal/config/postman.default.toml`:

| Field                              | Default  | Scope   | Used by                   |
| ---------------------------------- | -------- | ------- | ------------------------- |
| `notification_template`            | (greeting + filename + pop cmd) | Global | §2.1 |
| `enter_delay_seconds`              | `3.0`    | Global  | §2.1, §2.2                |
| `pane_notify_cooldown_seconds`     | `600`    | Global  | §2.2                      |
| `reminder_interval_messages`       | `20`     | Global + per-node | §2.2             |
| `reminder_message`                 | `"{inbox_path}"` | Global + per-node | §2.2   |
| `inbox_unread_threshold`           | `3`      | Global  | §2.3                      |
| `ui_node`                          | `""`     | Global  | §2.3, §2.4, §2.5          |
| `alert_cooldown_seconds`           | `600`    | Global  | §2.3, §2.4, §2.5          |
| `alert_delivery_window_seconds`    | `60`     | Global  | §2.3, §2.4, §2.5          |
| `alert_message_template`           | (envelope) | Global | §2.3, §2.4, §2.5         |
| `inbox_unread_summary_alert_template` | (see config) | Global | §2.3              |
| `node_inactivity_alert_template`   | (see config) | Global | §2.4                   |
| `unreplied_message_alert_template` | (see config) | Global | §2.5                   |
| `spinning_alert_template`          | (see config) | Global | §3.1                   |
| `alert_action_reachable_template`  | (see config) | Global | §2.3, §2.4, §2.5       |
| `alert_action_unreachable_template` | (see config) | Global | §2.3, §2.4, §2.5     |
| `dropped_ball_timeout_seconds`     | `0`      | Per-node | §2.5, §2.6               |
| `dropped_ball_cooldown_seconds`    | `0`      | Per-node | §2.6                     |
| `dropped_ball_notification`        | `"tui"`  | Per-node | §2.6                     |
| `dropped_ball_event_template`      | (see config) | Global | §2.6                   |
| `idle_timeout_seconds`             | (none)   | Per-node | §2.4                     |
| `node_idle_seconds`                | (none)   | Global  | §3.1                      |
| `node_spinning_seconds`            | `0`      | Global  | §3.1                      |
| `node_stale_seconds`               | `900`    | Global  | §3.1, §3.4 (cleanup)      |
| `heartbeat_message_template`       | (envelope) | Global | §2.7                    |
| `[heartbeat].llm_node`             | `""`     | Global  | §2.7                      |
| `[heartbeat].interval_seconds`     | (none)   | Global  | §2.7                      |
| `[heartbeat].prompt`               | (none)   | Global  | §2.7                      |

---

## 7. "Node Not Responding" Disambiguation

Three mechanisms can all fire when a node appears unresponsive, but they detect
subtly different conditions:

| Property              | Node inactivity (§2.4)                        | Unreplied message (§2.5)                      | Dropped ball (§2.6)                            |
| --------------------- | --------------------------------------------- | --------------------------------------------- | ---------------------------------------------- |
| What it measures      | No sends AND no receives for N seconds        | Message in `read/` for N seconds with no reply sent | `LastReceived > LastSent` for N seconds    |
| Data source           | `IdleTracker` timestamps                      | `read/` file modification times               | `IdleTracker` timestamps                       |
| Target                | `ui_node` (alert message)                     | `ui_node` (alert message)                     | TUI / status bar                               |
| Config trigger        | Per-node `idle_timeout_seconds`               | Per-node `dropped_ball_timeout_seconds`        | Per-node `dropped_ball_timeout_seconds`        |
| Liveness required     | No                                            | No                                            | Yes — fires only after liveness confirmed      |
| Rate limit            | `AlertRateLimiter` (shared with §2.3, §2.5)   | `AlertRateLimiter` (shared with §2.3, §2.4)   | Per-node `dropped_ball_cooldown_seconds`       |
| Excludes              | Nodes with `state: user_input` waiting file   | Daemon-generated messages (`from: postman`)   | Nodes without confirmed liveness               |
| Can fire simultaneously | Yes, if both conditions are met             | Yes, if both conditions are met               | Yes — completely independent delivery channel  |

**Key insight:** Node inactivity fires when a node is *generally* silent (no
sends, no receives). Unreplied message fires when a node *read* a message but
never replied. Dropped ball fires when a node *received* a message (delivery
confirmed) but never sent anything afterward. All three can fire for the same
node in the same tick if conditions align.

**Why they can overlap:**

- Node inactivity uses wall-clock idle time; the others use file timestamps.
- Dropped ball is suppressed by `AlertRateLimiter` independently (it uses its
  own cooldown, not the shared rate limiter).
- The `AlertRateLimiter` key is the recipient (`ui_node`), not the monitored
  node — so one node's inactivity alert can suppress another node's unreplied
  alert within the same cooldown window.

---

## 8. Flow Diagrams (ASCII)

### 8.1. Message Lifecycle and Notification Points

```text
Sender writes to post/
        |
        v
Daemon DeliverMessage()
        |
   [routing valid?]
   /           \
 No             Yes
  |              |
  v              v
dead-letter/   inbox/{recipient}/   <-- (A) Pane notification fires here
               |
               v
        SendToPane(bypassCooldown=true)
               |
               v
        Recipient reads, archives to read/
               |
               v
        ReminderState.Increment()   <-- (B) Reminder counter increments
               |
          [counter >= interval?]
          /            \
        No              Yes
         |               |
         |               v
         |        SendToPane(bypassCooldown=false)  <-- (B) Reminder fires
         |
         v
   IdleTracker updates LastReceived
```

### 8.2. 30-Second Tick: Alert Routing

```text
inboxCheckTicker (30 s)
        |
        |---> checkInboxStagnation() -------> [guards pass?] --> sendAlertToUINode()
        |                                                                |
        |---> checkNodeInactivity() --------> [guards pass?] --> sendAlertToUINode()
        |                                                                |
        |---> checkUnrepliedMessages() -----> [guards pass?] --> sendAlertToUINode()
        |                                                                |
        v                                                                v
  IdleTracker.CheckDroppedBalls()                                 post/{ui_node}
  [per-node cooldown pass?]                                             |
        |                                                               v
        v                                                     Daemon delivers to
  TUI events channel                                         inbox/{ui_node}/
  + tmux display-message (if configured)                             |
                                                                      v
                                                              SendToPane (ui_node)
```

### 8.3. "Node Not Responding" — Which Alert Fires

```text
Node received message?
  Yes --> LastReceived > LastSent for > dropped_ball_timeout_seconds?
            Yes + liveness confirmed --> DROPPED BALL (§2.6, TUI)
            Yes + liveness confirmed + ui_node configured --> UNREPLIED (§2.5, if read/)

Node has message in read/ > dropped_ball_timeout_seconds?
  Yes --> UNREPLIED MESSAGE ALERT (§2.5, ui_node)

Node idle (no send, no receive) > idle_timeout_seconds?
  Yes + no user_input waiting file --> NODE INACTIVITY ALERT (§2.4, ui_node)
```
