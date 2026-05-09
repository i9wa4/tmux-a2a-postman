# Daemon and TUI concurrency map

This document maps the daemon, daemon-submit, delivery, and TUI update paths so
the implementation can use Go concurrency where work is independent while
keeping shared state serialized where correctness depends on ordering.

## Ownership model

The daemon runtime owns mutable node/session state in a single event loop. That
loop is the serialization point for:

- known node state and node activity snapshots
- watcher directory registration and pruning
- route reservation decisions for post delivery
- TUI status and node activity event ordering
- projection updates that must be consistent with mailbox state

Work may run concurrently when it does not mutate this runtime state directly,
or when results return to the event loop before shared state is updated.

## Path inventory

| Component/path             | Mode after change                         | Trigger/cadence                         | Shared state                              | Blocking IO/tmux/filesystem calls                         | Backpressure                              | Safe parallel boundary                         |
| -------------------------- | ----------------------------------------- | --------------------------------------- | ----------------------------------------- | ---------------------------------------------------------- | ----------------------------------------- | ---------------------------------------------- |
| TUI raw daemon relay       | Serialized event read, latest snapshot    | daemon event channel                    | known session set local to relay          | TUI channel send                                           | snapshot replace, non-session drop        | health refresh requests are side-band          |
| Session health refresh     | Bounded per-session workers, latest gen   | status/config/activity/alive events     | atomic latest health generation           | `refreshProjectedSessionHealth`, tmux pane/window listing  | worker cap, stale generation drop         | emit only matching generation results          |
| Session scan tick          | Serialized apply                          | `sessionScanInterval` ticker            | previous session snapshot in runtime      | `discovery.DiscoverAllSessions`                            | blocking TUI status send                  | daemon-submit workers cannot block this tick   |
| Full scan tick             | Serialized apply                          | `ScanInterval` ticker                   | runtime nodes, known nodes, pane snapshots | node discovery, tmux pane state, projection scan           | event-loop ownership                      | collect IO before mutating runtime state       |
| Daemon-submit requests     | Bounded workers, per-session active guard | fsnotify create/rename, scan recovery   | active submit session map in event loop   | request claim/read, post write, inbox pop, projection sync | worker cap, per-session filesystem queue  | worker result returns to event loop            |
| Submit send post wake      | Serialized result handling                | daemon-submit worker result             | runtime post guards and route state       | post reconciliation, projection sync, node discovery       | event loop ordering                       | worker only writes response and post file      |
| Post reconciliation        | Serialized reservation                    | post fsnotify, submit result, backlog   | active post events, delivery route state  | projection sync, node discovery                            | same-route retry timer                    | pane delivery starts after reservation         |
| Pane delivery              | Goroutine per accepted delivery           | post reservation success                | route reservation completed in defer      | tmux notification, inbox/archive filesystem writes         | route gap, active post guard              | different accepted routes may run concurrently |
| Swallowed-message redrive  | Inline scan, delivery helper call         | inbox check ticker                      | idle tracker and daemon state             | inbox scans, pane notification                             | ticker cadence                            | notification buffer mutex protects tmux buffer |
| Auto-ping delivery         | Goroutine per active node                 | discovery, pane restart, startup        | active auto-ping map                      | tmux notification, auto-ping projection                    | per-node active guard                     | different nodes may ping concurrently          |

## Regression contracts

| Contract                         | Guardrail                                                                 |
| -------------------------------- | ------------------------------------------------------------------------- |
| Raw TUI session snapshots        | A blocked health probe must not delay a newer `status_update`.            |
| Health batch parallelism         | A slow session health probe must not block a fast session in the batch.    |
| Health latest-wins               | A stale health generation must not publish after a newer snapshot exists.  |
| Daemon-submit responsiveness     | A blocked submit worker must not block session status scan propagation.    |
| Daemon-submit durability         | Saturated workers leave request files pending for the next scan/watcher.   |
| Same-session submit ordering     | Only one daemon-submit worker per session may be active at a time.         |
| Post delivery completion         | Route reservation and post active guards prevent duplicate completion.     |
| Notification buffer correctness  | tmux `set-buffer`/`paste-buffer` remains protected by notification mutex.  |

## Serialized by design

The following sections should remain serialized unless they are moved behind an
explicit result channel back into the daemon event loop:

- mutation of `daemonRuntime.nodes`, `knownNodes`, previous session snapshots,
  and previous active pane snapshots
- watcher directory registration/removal
- route reservation and completion for post delivery
- same-route delivery completion and retry scheduling
- final TUI event ordering for node activity and session snapshots

Parallelizing these directly would risk data races, duplicate delivery
completion, stale node status, or lost watcher coverage.

## Parallelized paths

The Go-like concurrency shape is:

- use bounded workers for daemon-submit request processing
- leave daemon-submit request files unclaimed when workers are saturated so the
  next watcher or scan pass can pick them up
- keep only one daemon-submit worker active per session so inbox pops and
  projection writes for that session remain ordered
- return daemon-submit results to the daemon event loop before waking post
  reconciliation
- use bounded workers inside a session health refresh batch
- emit each session health result as it completes so one slow tmux check does
  not block unrelated health updates
- tag health refresh requests with a generation and drop stale results once a
  newer raw session snapshot has been observed
- keep raw session snapshots on the existing latest-wins TUI forwarding path

This keeps CPU and IO work concurrent without allowing unbounded goroutines or
direct shared-state mutation outside the owning loop.

## Backpressure rules

- Daemon-submit concurrency is capped by `daemonSubmitWorkerLimit`.
- Saturated daemon-submit workers apply filesystem backpressure: the request
  remains pending instead of being claimed and forgotten.
- Session health refresh concurrency is capped by
  `sessionHealthRefreshWorkerLimit`.
- TUI session snapshots retain latest-wins behavior when the TUI channel is
  full.
- Non-session health events may still be dropped if the TUI is not consuming;
  this prevents a stale UI client from blocking the relay.

## Unsafe parallelization boundaries

Do not parallelize these paths without adding an ownership handoff:

- concurrent writes to node state or watcher registrations
- concurrent route reservation for posts sharing sender, recipient, and session
- concurrent completion processing for the same post path
- concurrent mailbox projection writes for the same mailbox
- fire-and-forget daemon-submit goroutines that call post reconciliation
  directly

The safe pattern is worker goroutines for independent IO, followed by result
delivery back to the daemon event loop for stateful decisions.
