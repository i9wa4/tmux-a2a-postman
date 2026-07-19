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

Existing `discovery.NodeInfo.PaneID` remains a string for compatibility in this
issue. New backend-facing code converts it at the boundary with
`multiplexer.TmuxPaneID`.

## 4. Capture Boundary

Pane capture is represented by `multiplexer.PaneBackend.CapturePane`.

The tmux backend preserves existing capture forms:

- visible pane: `tmux capture-pane -p -t <pane>`
- recent scrollback: `tmux capture-pane -p -t <pane> -S -<tailLines>`
- retained history: `tmux capture-pane -p -t <pane> -S -`

The public `paneutil.Capture*` functions remain unchanged and delegate to the
tmux backend.

## 5. Current Context Boundary

This issue does not move current context resolution. Later work should use this
contract to separate:

- current identity lookup: backend kind, pane ID, session ID/name, node name;
- ownership/context checks: `ContextOwnsSession`, `FindSessionOwner`, and
  canonical status ownership.

Ownership-dependent behavior belongs to #656 before #654 generalizes current
context resolution.

## 6. Layout And Status Boundary

Issue #655 owns structural layout/status projection. This issue only records
that backend-neutral status should not require callers to parse tmux window or
pane command output directly.

Compatibility requirements for #655 include existing status JSON,
`SessionStatus.Compact`, and `get-status-oneline`.

## 7. Herdr Gates

Herdr access remains blocked:

- #660 must define read/write security gates, allowlists, protocol/schema
  checks, no-server error normalization, and licensing/compliance decisions.
- #658 may add read-only Herdr behavior only after the #660 read gate.
- #659 may add Herdr write/mutation only after #658 and #660.

Herdr read/write paths should include a pre-flight guard or equivalent
mechanical check so explicit issue-body blockers are enforced in code or local
workflow before activation.
