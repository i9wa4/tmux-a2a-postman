# Multiplexer Backend Contract

Issue: #653.

## 1. Purpose

This contract defines the first behavior-preserving backend seam for the existing tmux implementation. The current product supports only tmux; the new types keep backend identity, native resource IDs, pane capture, and runtime probing from spreading backend-specific parsing into higher-level call sites.

## 2. Current Backend

- `tmux` is the default and only active backend.
- Existing tmux command arguments, timing, and error behavior remain compatible.
- Herdr is not a runtime dependency for this issue.

## 3. Backend-Neutral IDs

Backend-neutral code should pass `multiplexer.ResourceID` values rather than parse native IDs directly. `Backend` is currently `tmux`; `Kind` identifies pane, session, window, or node; and `Native` is the backend-owned identifier. Existing discovery and `TMUX_PANE` values remain strings at the compatibility boundary, where they become `TmuxPaneID` or `IdentityTarget` values.

## 4. Capture Boundary

`multiplexer.PaneBackend.CapturePane` preserves visible capture (`capture-pane -p -t <pane>`), recent scrollback (`-S -<tailLines>`), and retained history (`-S -`). Public `paneutil.Capture*` functions remain unchanged.

## 5. Current Identity Boundary

`multiplexer.CurrentIdentity` carries backend, session name, node name, pane resource ID, and native evidence. The tmux resolver preserves canonical `%[0-9]+` `TMUX_PANE` validation, explicit lookup failures, compatibility wrappers, and one production runtime-context resolver so pane/session/node fields cannot drift. Herdr labels remain display/fallback information rather than authoritative identity.

## 6. Current Context Boundary

Current identity lookup remains separate from ownership/context checks (`ContextOwnsSession`, `FindSessionOwner`, and canonical status ownership). Ownership-dependent behavior belongs to #656 before #654 generalizes current context resolution.

## 7. Layout And Status Boundary

Issue #655 owns structural layout/status projection. Backend-neutral status must not require callers to parse tmux window or pane command output directly. Existing status JSON, `SessionStatus.Compact`, and `get-status-oneline` semantics remain compatible.

The #655 `SessionLayout` contract exposes ordered layout groups and items; for tmux, groups are windows and items are panes. The public payload retains legacy `windows` for existing consumers and adds backend-neutral `layout_groups`. Compact and one-line status derive from the same ordered tmux-compatible pane projection. Herdr may populate `layout_groups` after #660 allows reads and may derive compatibility windows, without making tmux windows authoritative or changing pane precedence before #639 resolves the semantic model.

## 8. Herdr Gates

Herdr access remains blocked. #660 must define read/write security gates, allowlists, protocol/schema checks, no-server error normalization, and licensing/compliance decisions. #658 may add read-only behavior only after the #660 read gate; #659 may add writes only after #658 and #660. Herdr paths require a pre-flight guard or equivalent mechanical check before activation.
