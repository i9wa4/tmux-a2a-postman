# Saved server status

`refresh-status` owns finite observation of one explicitly selected tmux server.
`get-status` returns the saved JSON bytes unchanged; `get-status-oneline`
renders that same document using the current clock for glyph aging. Neither
reader calls tmux, loads configuration, enumerates mail, creates directories,
takes locks, registers agents, refreshes, or writes invocation diagnostics.

## 1. Server and schema boundary

TMUX is `absolute-socket,PID,index`, split at the last two commas so socket
paths can contain commas. PID 0 explicitly means unknown. A nonzero caller PID
must match the saved/native server PID. No TMUX_PANE, cwd or guessed agent
identity is needed. `--config` is accepted for invocation compatibility but
never loaded by these three commands and cannot select a subset of a shared
server cache.

The version 1 document contains:

- `server`: selected `socket`, verified `canonical_socket`, `pid`, `incarnation`
  (PID:start-time:socket-device:inode:mtime-nanoseconds).
- `started_at`: collection start; `as_of`: collection/publication time. A getter
  does not change either or assert that the saved incarnation is still alive.
- `coverage`: enumerated pane count, successful new captures, reused
  observations, unavailable captures, `unknown_nodes`, complete flag and partial
  reason. Capture coverage and mail/config availability remain independent;
  row reasons explain missing facts. Either makes the standalone result partial.
- `sessions[]`: native `id` (`$N`), name and `windows[]`; windows retain native
  `id` (`@N`), numeric index, name, layout and physically ordered `panes[]`.
- Each pane: native `id` (`%N`), process `pid`, title, canonical HOME-relative
  `workspace` when available, top/left, dead/linked flags, `capture`, `status`,
  `scope` and optional reason. All sessions and ordinary panes remain visible.
- `capture`: actual `at`, nullable `changed_at`, fingerprint and immutable
  history filename; null if never captured. A failed due capture can retain the
  earlier observation with its original time plus a reason. This never makes it
  fresh.
- `status`: existing node summary (same fields as operation `self`), or null.
  Unknown counts stay null. Aggregate mail corruption is not fabricated zeroes.
- `scope`: registered configuration path/fingerprint, outgoing `edges`, local
  `interface_node` and declared `sessions` (name/parent/interface_node), or
  null.

At most 1024 native panes/4098 frame lines/256 KiB enumeration and 4 MiB saved
JSON are accepted. Exceeding a fatal bound preserves the previous snapshot and
reports an error. Names/paths containing native frame separators (tab/newline)
are rejected rather than misattributed. Spaces, quotes and socket commas are
ordinary data. No native session number is derived from a session name or an
array position. Empty/non-agent sessions retain their native `[N]` label in the
oneline display; windows with no agent status have no glyph.

Missing, unsafe, malformed or incompatible cache gives an explicit unavailable
JSON (or `?` for oneline), nonzero exit and stderr. A known PID mismatch is
rejected. A valid stale document remains dated: readers cannot discover a
restart with unknown PID, nor same-PID reuse, without a refresh. Operators must
not treat cached visibility as current liveness or permission.

## 2. Attribution and authority

Normal verified mutation admission saves per-pane configuration provenance in
that workspace's private runtime directory. Its native owner/process, absolute
source path and structural topology fingerprint are recorded. Refresh reads only
already registered workspaces identified by native pane directories and saved
bindings. It validates the referenced topology and compares all matching live
registered configuration sources. Configuration provenance uses only existing
atomic private runtime records; mail counts separately require a shared
workspace lock and rechecked binding. A busy mail writer leaves counts unknown
without erasing independently verified scope. Different/conflicting or changed
input is explicit unknown until normal operations establish consistent
provenance. The refresh caller cannot choose a global winner with `--config` or
cwd.

This adds no registry, mailbox, fake caller, send route or management grant.
New/legacy registrations lacking configuration provenance remain unknown until
a normal configured operation. Ordinary pane captures never create agent state.
A known node summary requires the exact saved binding, live server incarnation,
session, pane PID, root and unique matching configured title. Linked, dead,
renamed, ambiguous or unbound panes cannot inherit healthy facts.

Consumers first match their assigned server/session/native pane/workspace, then
use the attributed status/scope. Role names alone are ambiguous across sessions.
For interface adjacency with an explicit tree, select the local session
declaration in its own scope, then its direct parent/children and declared
interface roles. Without a tree, compare verified native interface pane roots
for nearest canonical parent/child containment. Missing/ambiguous native matches
are unknown; cached status alone does not authorize a send. There is no
top-level workspace/nodes/parent/children projection. Managers separately
establish direct reports from their assigned role policy. Global observation
does not authorize reading peer inbox bodies, controlling unrelated workspaces
or bypassing configured routes.

## 3. Bounded collection and concurrency

One two-second total context covers enumeration, a one-second capture sweep,
aggregation, final full native verification and publication. Native frames
retain the existing 200ms/60-KiB limit. At most 64 due panes are attempted per
sweep; missing/oldest observations go first. Each pane has a 30-second capture
cadence. Mail counts/layout are collected each successful admission, including
idle refreshes. Only an unchanged server/session/window/pane PID/root/title can
reuse an observation. Restarts, replacements and backwards clock observations
reset the comparison baseline. First/equal samples do not invent a change
timestamp.

Automatic window-name changes are display metadata, taken from the closing
frame. They never waive native process, role, session, directory or geometry
checks.

Unavailable/due panes remain due. Partial publication contains actual coverage,
old individual times and reasons; it never advances a whole-server capture
clock. Screen change is an observation, not task state, accepted input or proof
that an AI followed instructions. Raw text is bounded to 30 lines/8 KiB per
capture, stored separately with native ownership and hash. No retained history
is rescanned to build status. There is no expiry, pruning or rotation.

The lock is `refresh.lock`, an owned private regular file with one stable inode.
An OS advisory exclusive nonblocking flock is attempted once. Busy returns
`skipped_busy` immediately with exit 0; no wait, retry, queue or external flock
binary. The descriptor is close-on-exec; exit/death releases the kernel lock.
Separate atomic owner metadata records PID, invocation, acquisition time, target
and enumerate/capture/aggregate/publish phase. It may race, be missing or be
stale; it never proves lock ownership. Only actual contention plus an age beyond
the total budget and one-second allowance produces a long-held/suspected-stalled
warning. Invalid/future metadata reports unknown age. Never delete/replace the
lock, force-unlock, steal ownership or kill a process based on metadata.

## 4. Storage and failure boundaries

The existing application state root may be owned mode 0755, without group/other
write access. Its new `%status/<sha256(selected-socket)>/` namespace and
children are mode 0700, files mode 0600. `%status` cannot collide with encoded
workspace names (literal percent becomes `%25`). There is no permission
migration.

`status.json` is one logical publication: bounded schema validation,
same-directory exclusive private temporary file, file sync, rename, then
directory sync. A reader sees old or new complete JSON. Failures before rename
preserve the old snapshot; failures after rename explicitly report uncertain
durability. Owner metadata/history can exist after a failed publication; neither
replaces status. History is immutable and left for the user to delete. Old
workspace observation files are also retained but no longer written/read as the
current status index.

Standalone output is compact `updated`, `skipped_busy`, `partial` or `error`.
Partial/error is nonzero with stderr; busy is successful. Normal busy contention
has no stderr spam; exceptional owner/age uncertainty is diagnostic. External
status-bar composition must render the last cache even if refresh fails and
preserve diagnostics in a private destination, not suppress all stderr. No
resident collector, timer, per-client queue or implicit reader refresh exists.

## 5. Final writer/call-site map

| Path                                                                                | Responsibility / timing                                                                                                                   |
| ----------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| Public `refresh-status`                                                             | Same finite `Mail.RefreshServerStatus` runner, explicit server; no agent required                                                         |
| `finishMail`: send, POP, decision                                                   | Eligible operation then shared runner after primary locks/notification; empty POP included                                                |
| `execute-heredoc` completion                                                        | Same runner before operation-local self/output, after execution/mail locks; no effect replay                                              |
| External sync/report completion                                                     | Same runner after primary operations release locks                                                                                        |
| Verified normal mutation admission                                                  | Per-pane config provenance only, supplemental failures; no monitoring capture                                                             |
| `summary.go` / operation `self`                                                     | Fresh verified operation-local mail facts plus matching saved process capture clocks; never substitutes cached counts for primary results |
| `native_frame.readFrame`                                                            | Shared low-level bounded native read primitive                                                                                            |
| Notification paste/settle/C-m/confirmation                                          | Synchronous recipient-only safety reads via readFrame, independent of refresh lock/cadence/cache                                          |
| Former `CaptureSession`, capture admission/index, `OperatorStatus` getter collector | Removed; historical files retained without a second writer                                                                                |
| Both getters                                                                        | Read/validate one existing document; oneline pure glyph aging; no persistent diagnostic writer                                            |

A supplemental refresh failure never changes committed message IDs, recovery
IDs, claim paths, success/exit status or operation-local self. It emits a
warning; it never puts delivered mail into failed, asks for resend, or
re-executes effects.

## 6. Validation

Native Go regressions use private tmux sockets/HOME/state, never live sessions:
getters with a failing tmux proxy and unchanged filesystem, cadence/history,
config conflicts, partial capture and recovery, clock rollback, replacement,
separate sockets, native number gaps and two-digit labels, lock contention,
close-on-exec, atomic failure boundaries and existing mail/input/approval
suites. The invocation benchmark measures cached readers and idle/due refresh
alongside send/POP storms. CI runs native tests on Linux and macOS.
Deterministic terminal fixtures prove mechanics, not model compliance or
installed-fleet activation.
