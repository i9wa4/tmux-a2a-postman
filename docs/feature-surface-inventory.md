# Feature Surface Inventory

Compiled for issue #561 (inventory and remove unnecessary feature surfaces).
Captures the state of `main` HEAD at time of writing.

## 1. CLI Commands

| Command                 | Category            | Notes                                                               |
| ----------------------- | ------------------- | ------------------------------------------------------------------- |
| `send-heredoc`          | **Core**            | Compose and deliver via quoted heredoc stdin; primary send path     |
| `pop`                   | **Core**            | Claim and archive oldest unread inbox message                       |
| `get-status`            | **Core**            | Canonical session status JSON; shared by TUI, agents, and scripts   |
| `get-status-oneline`    | **Core**            | Compact all-session emoji status; used for quick agent coordination |
| `version`               | **Core**            | Build version JSON                                                  |
| `start`                 | **Core**            | Start daemon; always TUI mode (no-TUI hardcoded off — see §4)       |
| `stop`                  | **Core**            | Stop running daemon; idempotent                                     |
| `help [topic]`          | **Core**            | Help overview and topic pages                                       |
| `inspect-input`         | Optional/diagnostic | Inspect open reply-required work by id                              |
| `inspect-daemon-submit` | Optional/diagnostic | Inspect daemon-submit timeout state by id                           |
| `inspect-message`       | Optional/diagnostic | Inspect persisted message content by id                             |
| `capture-profile`       | Optional/diagnostic | Capture one explicit heap or goroutine profile from running daemon  |
| `send`                  | Deprecated/disabled | Body-argv disabled; returns shell-expansion safety guidance only    |

## 2. CLI Flags on `start`

| Flag           | Category | Notes                                                  |
| -------------- | -------- | ------------------------------------------------------ |
| `--context-id` | Core     | Context ID (auto-generated if omitted)                 |
| `--config`     | Core     | Path to config file (auto-detect from XDG_CONFIG_HOME) |

No user-facing `--no-tui` flag exists on `start`; the `NoTUI bool` field is
internal and hardcoded to `false` in `main.go:61`.

## 3. Config Fields (`postman.toml`)

| Field                           | Category | Notes                                                                                                        |
| ------------------------------- | -------- | ------------------------------------------------------------------------------------------------------------ |
| `edges`                         | **Core** | Bidirectional routing rules between nodes; required                                                          |
| `ui_node`                       | Optional | Startup PING target filter; prefer Mermaid class syntax                                                      |
| `auto_enable_new_sessions`      | Optional | Auto-enable sessions with configured node panes (default: true)                                              |
| `message_footer`                | Optional | Header guidance before the body separator in sent messages                                                   |
| `draft_template`                | Optional | Stored send-heredoc Markdown envelope                                                                        |
| `daemon_message_template`       | Optional | Structured envelope for daemon-originated PING mail                                                          |
| `skill_path`                    | Optional | Postman.md skill catalogs                                                                                    |
| `session_scan_interval_seconds` | Optional | Lightweight tmux session-list refresh (default: 0.10)                                                        |
| `auto_ping_delay_seconds`       | Optional | Delay before first auto-PING for newly appeared nodes (default: 20)                                          |
| `notification_template`         | Optional | Pane hint rendered when mail arrives                                                                         |
| `min_delivery_gap_seconds`      | Optional | Same-route delivery gap for duplicate control                                                                |
| `retention_period_days`         | Optional | Inactive runtime cleanup window (default: 30; 0 = disabled)                                                  |
| `pane_capture_tail_lines`       | Optional | Recent-line compaction scan (default: 100; Claude/Codex first/change captures may fall back to full history) |

## 4. Daemon Runtime Features

| Feature                                  | Category      | Notes                                                                                   |
| ---------------------------------------- | ------------- | --------------------------------------------------------------------------------------- |
| TUI dashboard (BubbleTea, single-column) | **Core**      | Session toggle, node/inbox status display, `q` to exit                                  |
| Filesystem watcher (fswatcher)           | **Core**      | Watches post/inbox/read/submit directories per node                                     |
| Auto-PING startup reconciliation         | **Core**      | Discovers nodes at startup; sends initial PING to `ui_node`                             |
| Daemon submit queue                      | **Core**      | Filesystem-based async request queue                                                    |
| Idle tracker / compaction-PING           | **Core**      | Detects idle panes; sends compaction PINGs                                              |
| Runtime diagnostics / memory snapshots   | **Core**      | Passive log every 10 min; scalar counters only                                          |
| Session management (multi-session)       | **Core**      | Enable/disable sessions via TUI                                                         |
| Config reload on change                  | **Core**      | Restart required to apply most config changes                                           |
| Node discovery (pane title-based)        | **Core**      | Derives node names from tmux pane titles                                                |
| No-TUI (headless) mode                   | **Dead code** | `NoTUI bool` in `cli.Config`; hardcoded `false` in `main.go:61`; never publicly exposed |

## 5. TUI Surfaces

| Surface                                 | Category | Notes                                            |
| --------------------------------------- | -------- | ------------------------------------------------ |
| Single-column dashboard                 | **Core** | Node list, session toggles, inbox count          |
| `Enter` key session toggle              | **Core** | Enable/disable a session                         |
| `q` key to exit                         | **Core** | Graceful daemon shutdown; equivalent to `stop`   |
| Status relay (`relayDaemonEventsToTUI`) | **Core** | Internal goroutine bridging daemon events to TUI |

## 6. Unmerged Remote Feature Branches (Not in `main`)

These branches exist on `origin` but have not been merged into `main`. The
associated GitHub issues are marked CLOSED, but the code is not present in
HEAD.

| Branch                                          | Issue | Feature                           | Decision Needed |
| ----------------------------------------------- | ----- | --------------------------------- | --------------- |
| `origin/issue-487-add-no-tui-deduped-auto-ping` | #487  | Headless auto-PING reconciliation | Merge or drop   |
| `origin/issue-488-add-watch-status-command`     | #488  | `watch-status` read-only client   | Merge or drop   |

Both branches extend the no-TUI/headless operator model. Because that model is
a removal candidate (§7), these branches should be explicitly abandoned or
reconciled before any no-TUI cleanup proceeds.

## 7. Removal Candidates

### 7.1. No-TUI / Headless / Systemd Mode

#### 7.1.1. Classification: Removable dead code

Evidence:

- `NoTUI: false` hardcoded in `main.go:61`; never set to `true` in any
  user-facing path
- `NoTUI bool` in `cli.Config` (`dispatch.go:9`), `noTUI` parameter in
  `RunStartWithFlags` (`start.go:116`)
- Three `noTUI` references in `start.go` (lines 450, 504, and signature)
- No-TUI execution path is `<-ctx.Done()` — blocks without a TUI
- No `--no-tui` flag exposed in help text or `help start`
- README contains no headless/no-TUI documentation

Complexity cost:

- `RunStartWithFlags` carries `noTUI bool` in its signature (affects
  `dispatch.go`, `start.go`, all callers)
- `cli.Config.NoTUI` adds a field and forces callers to explicitly pass `false`
- `relayDaemonEventsToTUI` branches on `noTUI` for the `tuiEvents` channel
- Ongoing merge conflict risk from unmerged branches #487/#488

**Recommendation**: Remove no-TUI dead code in a focused PR. See follow-up
issue #578.

### 7.2. `send` Command (Already Disabled)

#### 7.2.1. Classification: Removable if the safety-guidance intent is met by docs

Evidence:

- `send` command body-argv is disabled; it returns a static safety message
- Not a delivery path; `send-heredoc` is the canonical alternative
- Listed in `help commands` under the default operator surface, which may
  confuse operators

**Recommendation**: Evaluate in a separate focused issue.

### 7.3. Other Surfaces

No other surfaces are evaluated as removal candidates without further
discussion. The diagnostic commands (`inspect-input`, `inspect-daemon-submit`,
`inspect-message`, `capture-profile`) are low-maintenance and serve concrete
incident-response needs. Config fields are all optional and non-breaking.

## 8. Summary

| Count | Category                                        |
| ----- | ----------------------------------------------- |
| 8     | Core CLI commands                               |
| 1     | Deprecated/disabled CLI command (`send`)        |
| 4     | Diagnostic CLI commands                         |
| 13    | Config fields (1 core, 12 optional)             |
| 1     | Dead-code internal mode (no-TUI)                |
| 2     | Unmerged remote branches extending no-TUI model |

The clearest removal candidate is the no-TUI dead code. Follow-up issue #578
tracks the specific removal scope.
