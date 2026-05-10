# Schema and Event Terminology Migration

This document defines the compatibility policy for the remaining `health`
terminology after the public command rename from `get-health` /
`get-health-oneline` to `get-status` / `get-status-oneline`.

The command names are public operator UI. The JSON schema, journal event names,
projection replay, and TUI payload names are machine contracts. They must not be
renamed opportunistically.

## Current Contract Surfaces

The following names are intentionally still `health`-based in schema version 3:

- `status.SessionHealth` and `status.NodeHealth` in
  `internal/status/contract.go`
- `session_health_snapshot` journal events in
  `internal/projection/session_health.go`
- TUI payload names such as `session_health_update`
- internal collector filenames such as `internal/cli/session_health.go`
- design wording that describes the canonical health/status contract shared by
  `get-status`, `get-status-oneline`, and the TUI

These names are replay and consumer surfaces. A rename changes compatibility
even if command behavior is unchanged.

## Decision

Keep schema version 3 immutable.

Do not rename existing v3 JSON fields, Go contract types, or journal event
names in place. Treat `session_health_snapshot` as historical archive truth for
existing journal records.

As of #423, there is no concrete consumer that needs status-named aliases for
the v3 machine contract. The implementation decision is therefore to add no
alias fields, no parallel event family, and no schema bump in this slice.
`get-status` and `get-status-oneline` remain the public command names, while
the v3 JSON, journal, replay, and TUI payload contracts keep their existing
health-named surfaces.

Any future terminology cleanup must use an additive transition:

1. Introduce new status-named surfaces alongside the existing health-named
   surfaces.
2. Keep replay support for existing `session_health_snapshot` events.
3. Emit compatibility data until downstream consumers and tests have a defined
   migration window.
4. Bump the public machine contract only when compatibility behavior is
   explicit and tested.

## Migration Shape

Use this order if the project later chooses to migrate machine terminology.

### Phase 1: Add status aliases

- Add status-named aliases or parallel types for public JSON where needed.
- Keep v3 field names unchanged.
- Add tests proving old `health` names and new `status` names can coexist.
- Do not change journal event emission yet.

Definition of done:

- Existing v3 consumers still pass unchanged.
- New status-named aliases are documented as additive.
- No replay behavior changes.

### Phase 2: Add parallel journal event family

- Add a status-named snapshot event only if a concrete consumer needs it.
- Continue replaying `session_health_snapshot`.
- If both event families are present, prefer the newer status event for the
  same generation and timestamp, but keep health events valid.

Definition of done:

- Projection replay tests cover old health-only journals.
- Projection replay tests cover mixed health/status journals.
- Archive compatibility is documented.

### Phase 3: Contract version bump

- Bump `schema_version` only after aliases and replay compatibility are already
  in place.
- Document the changed fields and unchanged compatibility behavior.
- Update `get-status`, `get-status-oneline`, TUI contract tests, and design
  docs together.

Definition of done:

- Versioned contract tests show v3 compatibility and the new schema behavior.
- Help text and design docs describe the supported version boundary.
- No existing journal archive is unreadable.

## Non-Goals

- No immediate rename of `status.SessionHealth`, `status.NodeHealth`, or
  `session_health_snapshot`.
- No rewrite of existing journal archives.
- No silent change to `schema_version: 3`.
- No compatibility behavior inferred from command names alone.

## Test Strategy

Any implementation PR for this migration must update or add:

- status contract tests for versioned JSON fields
- projection replay tests for existing `session_health_snapshot` events
- mixed replay tests if a parallel status event is introduced
- TUI contract tests for event payload names
- documentation tests that keep command terminology and machine terminology
  distinct

## Issue Sequencing

This design unblocks small, explicit implementation issues. Recommended slices:

1. Add documentation-only wording that v3 health terminology is immutable.
2. Add status aliases only where a real consumer needs them.
3. Add a parallel event family only after a consumer requires it.
4. Bump the schema version only after replay compatibility and tests are in
   place.

Until those slices exist, code should keep the current health-named machine
contracts.
