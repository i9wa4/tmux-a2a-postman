# Multiplexer Backend Contract

Issue: #653.

## 1. Purpose

This contract defines the first behavior-preserving backend seam for the
existing tmux implementation. The current product still supports only tmux; the
new types keep backend identity, native resource IDs, pane capture, and runtime
probing from spreading backend-specific parsing into higher-level call sites.

## 2. Current Backend

- `tmux` is the default and only active backend.
- Existing tmux command arguments, timing, and error behavior remain compatible.
- Herdr is not a runtime dependency for this issue.

## 3. Backend-Neutral IDs

Backend-neutral code should pass `multiplexer.ResourceID` values rather than
parse native IDs directly.

- `Backend`: the multiplexer implementation, currently `tmux`.
- `Kind`: resource category such as `pane`, `session`, or `node`.
- `Native`: the backend-owned identifier, such as a tmux `%NN` pane ID.

Existing `discovery.NodeInfo.PaneID` and `TMUX_PANE` values remain strings for
compatibility in this issue. Backend-facing identity code converts them at the
tmux compatibility boundary with `multiplexer.TmuxPaneID` and passes
`multiplexer.IdentityTarget` values rather than raw native strings.

## 4. Capture Boundary

Pane capture is represented by `multiplexer.PaneBackend.CapturePane`.

The tmux backend preserves existing capture forms:

- visible pane: `tmux capture-pane -p -t <pane>`
- recent scrollback: `tmux capture-pane -p -t <pane> -S -<tailLines>`
- retained history: `tmux capture-pane -p -t <pane> -S -`

The public `paneutil.Capture*` functions remain unchanged and delegate to the
tmux backend.

## 5. Current Identity Boundary

Current identity is represented by `multiplexer.CurrentIdentity`:

- `Backend`: the source backend, currently `tmux`.
- `SessionName`: the logical postman session scope.
- `NodeName`: the logical postman node name.
- `Pane`: the backend-neutral pane resource ID.
- `NativeIDs`: backend-native evidence, such as tmux pane ID, session name, and
  pane title.

The tmux backend preserves existing lookup behavior:

- `TMUX_PANE` targets session-name and pane-title lookups only when it is a
  canonical tmux pane ID token, `%[0-9]+`.
- Untargeted `display-message` remains the fallback when `TMUX_PANE` is absent.
- `TMUX_PANE` itself remains the current pane ID when present.
- Invalid `TMUX_PANE` values fail closed with identity lookup errors instead of
  falling back to focused-pane discovery; this prevents generic tmux `-t` target
  expressions from forging sender or receiver runtime identity.
- Lookup failures are explicit `IdentityError` values at the backend boundary.
  Blank `pane_id`, `session_name`, and `pane_title` outputs are lookup failures.
  Compatibility wrappers still return empty strings to preserve existing CLI
  behavior.
- Production runtime-context send/pop paths consume one `CurrentIdentity`
  resolver so pane, session, and node fields do not drift across independent
  tmux lookups. CLI tests may still inject legacy tmux-named hooks; that seam is
  compatibility-only and should not be used for new backend code.

Herdr support should keep the same logical `session:node` address shape while
resolving internally through Herdr named session, workspace ID, tab ID, and pane
ID. Herdr labels are display/fallback information, not authoritative identity.

## 6. Current Context Boundary

Current context resolution must stay separated from current identity lookup:

- current identity lookup: backend kind, pane ID, session ID/name, node name;
- ownership/context checks: `ContextOwnsSession`, `FindSessionOwner`, and
  canonical status ownership.

Ownership-dependent behavior belongs to #656 before #654 generalizes current
context resolution.

## 7. Layout And Status Boundary

Issue #655 owns structural layout/status projection. This issue only records
that backend-neutral status should not require callers to parse tmux window or
pane command output directly.

Compatibility requirements for #655 include existing status JSON,
`SessionStatus.Compact`, and `get-status-oneline`.

Issue #655 adds a backend-owned `SessionLayout` contract with ordered layout
groups and items. For tmux, those groups are tmux windows and the items are
panes. The public status payload keeps the legacy `windows` projection for
existing tmux JSON/TUI consumers and adds `layout_groups` as the backend-neutral
structural view. `SessionStatus.Compact` and `get-status-oneline` continue to
derive from the same ordered tmux-compatible pane projection, so their semantics
do not change here.

First-phase Herdr support should omit tmux-style `windows` as an authoritative
native shape. After #660 allows Herdr reads, #658 may populate
`layout_groups` from Herdr workspace/tab/pane layout data and may optionally
derive compatibility `windows` groups for existing UI consumers. That projection
must stay clearly marked as compatibility output and must not introduce pane
state precedence changes before #639 resolves the semantic model.

Issue #658 adds a disabled-by-default Herdr read-only backend spike documented
in [Herdr Read-Only Discovery Spike](herdr-readonly-discovery-spike.md). The
spike uses backend-neutral `layout_groups` with Herdr tab groups and marks tmux
`windows` as unsupported native evidence instead of treating tabs as tmux
windows.

## 8. Interactive Delivery Boundary

Issue #657 separates interactive pane input from filesystem mailbox delivery.

- Interactive delivery is represented by
  `controlplane.InteractiveDeliveryAdapter`. The tmux implementation keeps using
  tmux pane input through `notification.PaneSender`, which preserves the
  existing set-buffer, paste-buffer, C-m timing, cooldown, retry, and
  sanitization behavior.
- Filesystem inbox writes and mailbox projection sync are represented by
  `controlplane.SystemMessageDeliveryAdapter` and the backend-neutral
  `controlplane.FilesystemSystemMessageAdapter`.
- The legacy `controlplane.HandAdapter` interface embeds both contracts for
  compatibility while call sites are split over later issues.

Issue #659 adds a disabled-by-default Herdr runtime bootstrap. Empty backend
metadata still resolves to tmux. When `[postman.herdr]` is enabled, startup uses
the configured Unix socket path to create a Herdr socket client, performs gated
Herdr discovery, adds Herdr panes to the live `discovery.NodeInfo` map with
backend/runtime metadata, registers pane-specific Herdr hand adapters, and
registers Herdr ownership mutation routing before delivery. Herdr targets still
fail closed if their pane was not discovered and registered by this bootstrap.
Each Herdr rediscovery reconciles the registered pane set and unregisters panes
missing from the current snapshot, so stale Herdr panes stop receiving delivery
or ownership mutations before daemon shutdown.

If tmux and Herdr expose the same logical `session:node` key in one discovery
pass, the already-discovered node wins and the later backend is reported as a
collision instead of silently overwriting the routing target. Current startup
and daemon scans discover tmux before Herdr, so this fails closed toward the
existing tmux route for cross-backend duplicates. Same-backend collision
selection remains backend-owned: tmux keeps numeric pane winner semantics, and
Herdr reports duplicate logical node claims from its snapshot.

Session ownership marker reads, writes, and shutdown clears are selected per
session. Non-Herdr sessions use `TmuxBackend`; only the configured Herdr session
uses the Herdr ownership mux. Startup activation and preclaim/reclaim paths that
enumerate tmux panes remain tmux-only in #659 and are not a general Herdr
session activation lifecycle.

Herdr interactive delivery uses the same runtime-aware submit-count resolution
as tmux delivery. When the target brain runtime is Codex and `enter_count` is
unset, Herdr sends two fixed submit key mutations. The Herdr key-combo mutation
uses `C-m`, matching the tmux path's documented Codex submit behavior instead
of the literal `Enter` key name that tmux avoids for Codex multiline readline.

## 9. Herdr Gates

Herdr access remains blocked until the gates in
[Herdr Security And Licensing Gates](herdr-security-licensing-gates.md) pass:

- #658 may add read-only Herdr behavior only after calling the #660 read gate
  for socket/session/workspace allowlists and protocol/schema checks. Discovery
  reads use the default discovery scope; pane-targeted reads must opt into the
  pane scope and provide tab/pane identity.
- #659 may add Herdr write/mutation only after #658 validates read-only behavior
  and the #660 write gate composes the pane-targeted read gate, confirms input
  sanitization, and confirms compliance decisions.

Herdr read/write paths use `multiplexer.ValidateHerdrReadGate` or
`multiplexer.ValidateHerdrWriteGate` as their preflight guard before consuming
Herdr data or issuing Herdr mutations. Write paths also revalidate the
configured pane against the Herdr session snapshot before text input or marker
metadata mutation. Session ownership metadata is keyed by logical postman
session name under `postman.session_owner.<session>`, preserving tmux's
per-logical-session isolation within an allowlisted Herdr workspace.
