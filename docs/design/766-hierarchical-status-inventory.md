# #766: hierarchical status/decision-needed projection inventory (first slice)

Bounded, read-only inventory of the existing status-projection contract,
identifying the smallest missing capability for #766's own acceptance-
criteria bar ("a top-level consumer can obtain a bounded view of work and
decisions across an explicitly declared three-level subtree"). This slice
is design/inventory only; it does not implement the gap it identifies.

## 1. What already exists (reused, not duplicated)

`internal/status/contract.go`'s `type SessionStatus struct` (lines 235-255)
is already a rich, single-session projection, reused as-is:

- `SchemaVersion`, `ContextID`, `SessionName`, `NodeCount`, `VisibleState`,
  `Severity`/`SeveritySource`/`SeverityReason`, `Compact`/`CompactSeverity`
  -- session-level identity and health.
- `Queues` (`SessionQueues`), `Delivery` (`*DeliveryStatus`) -- transport
  facts, already distinct from task/review state.
- `Tasks []TaskRunProjection` (lines 257-265): `TaskID`, `RunID`,
  `OriginatingMessageID`, `ThreadID`, `AssignedNode`, `LatestMessageID`,
  `OpenInputRequestIDs`, `State` -- this is already exactly the "logical
  session, external task/run identity, responsible node, pending
  dependencies" shape #766 asks for, scoped to one session.
- `WorkspaceTree *WorkspaceTreeStatus` (lines 205-210): `Current`
  (`*WorkspaceTreeNodeStatus`), `Parent` (`*WorkspaceTreeRef`), `Children
  []WorkspaceTreeRef`, `Diagnostics`. `WorkspaceTreeRef` (lines 181-185) is
  `{SessionName, Label, ID}` -- a reference only, not a fetched status.
- `CommandApproval *CommandApprovalStatus`, `Nodes []NodeStatus`,
  `LayoutGroups`, `Windows` -- node/pane-level detail, already seen live
  this session via `get-status` output (per-node `severity`,
  `node_local`/`flow` sub-objects with explicit `evidence_level`/
  `evidence_source`, `convention_meter`, `request_satisfaction` --
  including the exact "unresolved work stays explicitly unknown, not
  silently healthy" property #766's acceptance criteria require).

These are reused as-is; #766 does not need to redefine single-session
status, task/run projection, or evidence-level/freshness marking, which
already exist with the right shape.

## 2. The gap: declared hierarchy is referenced, not aggregated

`WorkspaceTreeStatus.Children` is a list of `WorkspaceTreeRef` --
`{SessionName, Label, ID}` pointers into the declared hierarchy (#504/#700's
own authorization work). Nothing in the current `SessionStatus` contract
fetches a child reference's own `SessionStatus` and composes it into the
parent's view. `get-status` as it exists today answers "what is the state
of THIS session," never "what is the state of this session and its
declared descendants." This is exactly #766's first acceptance criterion:
a bounded three-level subtree view does not exist yet; only the one-hop
parent/children *reference* list does.

## 3. Recommended minimal addition (not implemented in this slice)

A new, explicitly bounded aggregation surface -- not a change to
`SessionStatus` itself -- that:

- Takes an explicit depth bound (matching #766's own "explicitly declared
  three-level subtree," not unrestricted recursive discovery) and a
  starting session.
- For each declared child (`WorkspaceTreeStatus.Children`), fetches that
  child session's own `SessionStatus` (reusing the existing single-session
  collector, `sessionStatusCollector` in `internal/cli/session_status.go:82`
  from this inventory's own grep), and reports explicit per-child
  `fetched`/`unavailable`/`stale` state rather than omitting a child
  silently on any read failure (#766's "child unavailability ... [does not]
  render as healthy completion" criterion).
- Does NOT invent a new acceptance/review state machine: `Tasks[].State`
  and the existing `node_local`/`flow` evidence-level distinction already
  separate "acknowledged" from "reviewed" from "accepted"; the aggregator
  only composes these facts across sessions, it does not reinterpret them.
- Separately: a compact "decision-needed" report shape (distinct from the
  full aggregated status) that an orchestrator fills from its own evidence
  -- the pending item, why it cannot proceed locally, options/consequences,
  a recommendation, and references back to the exact pending durable
  record (reusing #765's correlation fields once that lands, rather than
  inventing a second identity scheme).

## 4. Why this slice does not implement the aggregator yet

A correct bounded cross-session fetch must reuse #624/#700's existing
authorization checkpoint (so a parent cannot read a child's status without
going through the same authorization path as any other cross-session
exchange) and #765's not-yet-landed origin-reference fields (so an
aggregated report can cite exactly which originating request a child's
work traces back to). Building the aggregator before either of those
exists would either skip authorization or fabricate a correlation
mechanism duplicating #765's own design. This inventory documents the
dependency explicitly rather than building around it.

## 5. Next slice (not this one)

1. Land #765's `OriginContextID`/`OriginMessageID` fields (prerequisite for
   citing cross-session provenance in an aggregated report).
2. Implement the depth-bounded aggregator described in section 3, with
   explicit per-child `fetched`/`unavailable`/`stale` states and a focused
   fixture across three sessions (reusing whatever fixture #765 builds for
   its own 3-level trace, per #766's own "growing the fixture alongside
   them" sequencing note in #764's issue body).
3. Define the compact decision-needed report shape and wire it to route
   through the mouthpiece designation #764 establishes.
4. Address deduplication/coalescing for repeated unchanged alerts (#766's
   escalation-and-aggregation scope item) as a further slice; not scoped
   or claimed solved here.

## 6. Explicit non-claims

- This slice adds no code and changes no runtime behavior.
- It does not close any #766 acceptance-criteria checkbox.
- The aggregator's exact shape is a recommendation from this inventory, not
  a locked-in decision; it should be reviewed before the next slice
  implements it, and explicitly depends on #765 landing first.
