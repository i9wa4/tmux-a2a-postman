# Node State Machine

Design document for the per-node state machine introduced in Issue #286.

## States

| State       | Color  | ANSI | TTY | Non-TTY | Description                                        |
| ----------- | ------ | ---- | --- | ------- | -------------------------------------------------- |
| ready       | Green  | 10   | ●   | 🟢      | Node active, no pending inbox messages             |
| pending     | Cyan   | 51   | ●   | 🔷      | Inbox message waiting, not yet archived            |
| composing   | Blue   | 33   | ●   | 🔵      | Node actively composing a reply                    |
| spinning    | Yellow | 226  | ●   | 🟡      | Node running long (past NodeSpinningSeconds)       |
| stalled     | Red    | 196  | ●   | 🔴      | Node went stale while composing or spinning        |
| user_input  | Purple | 141  | ●   | 🟣      | Node waiting for human input                       |

## State Transitions

```mermaid
stateDiagram-v2
    [*] --> ready
    ready --> pending : inbox/ message arrives
    pending --> ready : message archived
    pending --> composing : node sends reply
    ready --> composing : node sends message
    composing --> spinning : NodeSpinningSeconds elapsed, pane still active
    composing --> stalled : pane goes stale (composing window expired)
    spinning --> stalled : pane goes stale
    spinning --> ready : node replies
    stalled --> composing : node resumes (manual nudge or auto-nudge)
    ready --> user_input : node requests human input
    user_input --> ready : human responds
```

## Transition-To-Surface Inventory

| Transition             | Outward Effect                                                        | Surface(s)              | Current Overlap / Note                                                                 |
| ---------------------- | --------------------------------------------------------------------- | ----------------------- | -------------------------------------------------------------------------------------- |
| `[*] --> ready`        | Node appears as `ready` once discovered and no waiting overlay applies | TUI / oneline           | No inbox or pane notification is emitted for the ready baseline itself                |
| `ready --> pending`    | Unread file appears in `inbox/{node}/` and the recipient gets a pane hint | inbox + pane + TUI / oneline | One delivery creates both an inbox-visible unread item and an immediate pane notification |
| `pending --> ready`    | Unread file is archived and the node returns to `ready`               | inbox + TUI / oneline   | No dedicated follow-up message is emitted; the visible change is the cleared unread state |
| `pending --> composing` | Waiting state enters `composing` while the node starts replying      | TUI / oneline           | No separate daemon alert is emitted by the transition itself                          |
| `ready --> composing`  | Waiting state enters `composing` after the node sends a message       | TUI / oneline           | Same blue composing surface as above; the transition itself stays internal            |
| `composing --> spinning` | Node crosses the spinning threshold and turns `spinning`            | TUI / oneline + inbox   | Current overlap is explicit: the yellow spinning surface is paired with a `ui_node` spinning alert |
| `composing --> stalled` | Node goes stale while composing and turns `stalled`                  | TUI / oneline           | No dedicated inbox alert is emitted by this transition alone                          |
| `spinning --> stalled` | Node goes stale after spinning and stays visible as `stalled`         | TUI / oneline           | Any earlier spinning alert may already exist, but the stalled transition itself is only a surface change |
| `spinning --> ready`   | Reply activity clears `spinning` and returns the node to `ready`      | TUI / oneline           | The reply may create inbox-visible effects elsewhere, but this transition emits no separate notification |
| `stalled --> composing` | Recovery returns the node from `stalled` to `composing`              | TUI / oneline           | Manual nudge or auto-nudge changes the surface, but no recovery alert is emitted      |
| `ready --> user_input` | Human-facing prompt is active and the node turns `user_input`         | inbox + pane + TUI / oneline | Current overlap is intentional: the prompting message is inbox-visible while the purple dot suppresses inactivity alerts |
| `user_input --> ready` | Human-input wait clears and the node returns to `ready`               | TUI / oneline           | No dedicated completion alert is emitted by the transition itself                     |

## Time-Based Parameters

| Parameter            | Default | Description                              |
| -------------------- | ------- | ---------------------------------------- |
| NodeActiveSeconds    | 300s    | Idle duration before node transitions from ready to stale (first threshold) |
| NodeIdleSeconds      | 900s    | Idle duration before node is marked stale (second threshold) |
| NodeSpinningSeconds  | 0       | Seconds composing before transitioning to spinning (0 = disabled) |

## Implementation Files

| Layer        | File                            | Key Sections                                      |
| ------------ | ------------------------------- | ------------------------------------------------- |
| Daemon       | internal/daemon/daemon.go       | replaceWaitingState, worstStatePriority, collectPendingStates |
| TUI          | internal/tui/tui.go             | waitingStateRank, getSessionWorstState, updateNodeStatesFromActivity, node render switch |
| Oneline      | main.go                         | statusDot, applyWaitingOverlay, applyPendingOverlay |
| Config       | internal/config/config.go       | NodeSpinningSeconds                               |

## Design Decisions

### Internal vs. Display State

`statusForState()` in `internal/idle/idle.go` returns `"active"`, `"idle"`, or
`"stale"` for daemon transition logic. These internal values are preserved
unchanged. The display layer maps:

- `"active"` / `"idle"` from pane-activity.json -> `"ready"` via
  updateNodeStatesFromActivity
- waiting/ file states overlay the display layer via waitingStateRank /
  waitingOverlayRank

### Backward Compatibility

- `pane-activity.json` still emits `"active"` and `"idle"` from the idle
  tracker; `statusDot()` and the TUI node render switch accept these as aliases
  for `"ready"`.
- Old waiting files containing `"state: stuck"` are accepted as aliases for
  `"stalled"` in all switch cases.
