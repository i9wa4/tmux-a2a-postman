# Ping Events

Daemon PING mail is a stored inbox message from `postman` with
`messageType: ping`, `replyPolicy: none`, a `# Ping` heading, and the daemon
body `PING from postman daemon. Do NOT reply to this message.` It still follows
the normal mailbox rule: claim it with `tmux-a2a-postman pop` and read the
complete archived Markdown body before deciding that no action is needed.

All daemon PING mail is written through the common PING sender, so it also
sends the normal pane notification when inbox delivery succeeds.

## 1. Events

### 1.1. Startup Auto-PING

- Trigger: daemon bootstrap sees already discovered nodes.
- Timing: queued at startup, due after `auto_ping_delay_seconds` (default
  `20`), then delivered on the next full scan.
- Source: daemon startup.
- Recipients: all discovered nodes unless `ui_node` is explicitly set.
  Explicit `ui_node` limits startup auto-PING to matching roles discovered in
  enabled sessions.
- Notes: journal reason is `startup`. The embedded default
  `ui_node = "messenger"` does not narrow by itself.

### 1.2. New Node Auto-PING

- Trigger: a daemon scan, post wake-up, or session activation discovers a node
  that was not in the known-node set.
- Timing: queued when discovered, due after `auto_ping_delay_seconds`, then
  delivered on the next full scan.
- Source: daemon discovery.
- Recipients: the newly discovered node.
- Notes: journal reason is `discovered`. A node that disappeared and later
  returns is treated as newly discovered.

### 1.3. Replacement Pane PING

- Trigger: the same node key reappears with a new pane ID after its old pane
  exits.
- Timing: queued when the pane restart is detected, due after
  `auto_ping_delay_seconds`, then delivered on a full scan.
- Source: daemon pane-state scan.
- Recipients: the restarted node.
- Notes: journal reason is `pane_restart`. The TUI also gets a
  `pane_restart` event.

### 1.4. Operator TUI PING

- Trigger: the operator presses `p` on the selected session in the daemon TUI.
- Timing: sent immediately after cached or fresh discovery. If needed, the
  command first activates that session for this daemon.
- Source: user/operator.
- Recipients: every discovered node in the selected tmux session.
- Notes: not limited by startup `ui_node`. During the startup auto-PING delay,
  the TUI disables `p` and shows a readiness countdown instead of dispatching a
  manual PING. If the session is owned by another daemon, the send is blocked.

### 1.5. Compaction Recovery

- Trigger: pane capture detects a newer Claude or Codex compaction marker.
- Timing: checked every `pane_capture_interval_seconds` (default `5`) and
  limited by a `30s` per-pane cooldown.
- Source: daemon pane capture.
- Recipients: the node whose pane showed the newer compaction marker.
- Notes: the first observed marker is treated as baseline. Later newer markers
  can send compaction-triggered PINGs.

`auto_ping_delay_seconds = 0` makes queued auto-PINGs due immediately. With the
default full-scan interval of `scan_interval_seconds = 1`, a due auto-PING is
normally delivered on the next full scan. If delivery is retryable, for example
because the target inbox queue is full, the pending auto-PING remains in the
journal and the daemon tries again on later scans. One in-flight auto-PING per
node is allowed at a time.

## 2. Non-PING Traffic

Some traffic can look like ping noise but does not create daemon PING mail.

- Pane notification: a pane hint tells the recipient that stored mail exists.
  It is not the mail body; use `pop` to inspect the archived message.
- Swallowed-message redelivery: the `30s` inbox check can resend a pane
  notification for old non-daemon inbox mail when
  `delivery_idle_timeout_seconds` is set.
- TUI status updates: events such as `PING sent`, unread counts, or session
  status updates are daemon UI state, not inbox mail.
- Terminal no-reply text `PING`: a user-authored message whose first line is
  `PING` is terminal no-reply text, but it is not daemon PING mail unless its
  stored metadata says `messageType: ping`.
- Startup or session activation: activation claims panes and enables session
  directories. The activation step itself does not send mail; discovery can
  queue a later auto-PING.

## 3. `skill_path.inject`

`skill_path.inject` controls where generated skill catalogs are appended. It
does not create new ping events.

- Omitted or empty `inject`: normal role context only, not daemon PING mail.
- `inject: ping`: every daemon PING path above, including startup,
  discovered-node, pane-restart, operator TUI, and compaction.
- `inject: compaction_ping`: only compaction-triggered daemon PINGs.
- A YAML list containing `ping` and `compaction_ping`: the same selected
  catalog is routed to both targets. Compaction PINGs receive both every-PING
  and compaction catalogs.

PING catalog paths must be explicit user-level paths such as `~/...` or
absolute paths. Repo-local relative paths remain valid only for normal role
catalogs.

## 4. Noise Checks

Expected PING mail should line up with one of the events above. Check the
stored message metadata and daemon logs before treating it as suspicious:

- `messageType: ping`, `from: postman`, and `replyPolicy: none` identify daemon
  PING mail.
- Auto-PINGs should have a matching pending/delivered journal event with reason
  `startup`, `discovered`, or `pane_restart`.
- Compaction PINGs should follow a new compaction marker and should not repeat
  faster than the per-pane cooldown.
- Operator TUI PINGs should match a recent `p` keypress on the selected session.
- Repeated pane hints without new inbox files usually indicate notification
  redelivery, not new PING mail.

Repeated PING mail that has no startup, discovery, pane restart, operator
keypress, compaction marker, or retryable-delivery explanation is unexpected
and should be investigated with `tmux-a2a-postman get-status` and the archived
message IDs.
