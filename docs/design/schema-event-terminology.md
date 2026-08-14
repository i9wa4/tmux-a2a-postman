# Schema and Event Terminology

Schema version 4 is the terminology cutover for the canonical status contract.
Schema version 5 adds per-node convention-meter figures while preserving the
status terminology. The public commands were already `get-status` and
`get-status-oneline`; the machine-facing contract now uses status names too.

## 1. Canonical Surfaces

Current writers and live machine consumers use:

- `status.SessionStatus`, `status.NodeStatus`, `status.AllSessionStatus`, and
  related status-named structs in `internal/status/contract.go`
- `schema_version: 5`
- `session_status_snapshot` journal events
- `session_status_update` TUI events
- TUI event detail key `status`
- status-named projection and collector files such as
  `internal/projection/session_status.go` and `internal/cli/session_status.go`

Do not add health-named aliases for the v4 contract. A status consumer should
not need to know the old type names, TUI detail key, or current writer event
name.

## 2. Legacy Archive Replay

The only retained health-named machine literal is
`session_health_snapshot`, and it is retained solely as a read-only archive
reader for journals written before the v4 cutover.

Replay behavior:

- new writers emit only `session_status_snapshot`
- old `session_health_snapshot` records can seed projection when no current
  status snapshot has been seen for the same session generation
- if both event families exist, `session_status_snapshot` wins
- projected archive payloads are normalized to the current schema version

This is archive replay support, not public compatibility. Do not document or
emit `session_health_snapshot` as a supported producer surface.

## 3. Test Strategy

Status contract changes should update:

- status contract tests for schema version and status-named Go types
- projection tests for current status snapshots
- projection tests for read-only legacy archive replay
- mixed replay tests proving status snapshots win over legacy health snapshots
- TUI tests for `session_status_update` with the `status` detail key
- help, design, and skill text that describes command output
