# #765: request provenance and return-path inventory (first slice)

Bounded, read-only inventory of the existing message/thread/correlation
contract, identifying the smallest missing durable relationship needed for
"a three-level request can be traced from intake through each delegation and
back to the originating participant using durable records" (#765's own
acceptance-criteria bar). This slice is design/inventory only; it does not
implement the gap it identifies.

## 1. What already exists (reused, not duplicated)

`internal/envelope/metadata.go` (`type Metadata struct`, lines 12-48) defines
every correlation field currently carried in a message's YAML frontmatter:

- `ContextID` -- groups messages within one conversation, scoped to a single
  session's own mailbox exchange.
- `MessageID` -- unique per message.
- `From` / `To` -- the real sender/recipient at this one hop (`ParseMetadata`,
  `internal/message/message.go:542-543`'s `MessageInfo.From/To` mirror this
  at the filename-parsing layer).
- `ReplyTo` -- a single-hop pointer to the one message this message replies
  to (parsed at `metadata.go:245`, canonicalized at `metadata.go:648-649`,
  aliased at `metadata.go:715-716`).
- `ThreadID` -- used for command-approval correlation
  (`internal/message/message.go:246-249`'s `approvalDeliveryEvent.ThreadID`,
  derived via `mailboxThreadIDFromContent`/`metadata.ThreadID` at
  `message.go:191,252-256`).
- `TaskID`, `RunID`, `InputRequestID`, `FillsInputRequestID`,
  `InputRequestSetID` -- task/run and input-request-fill correlation within
  one exchange.
- `RuntimeContextID` / `RuntimeContextScope` / `RuntimeContextCapturedAt` /
  `RuntimeContextHash` -- the sender/receiver runtime-context snapshot
  mechanism (seen live in every message this session: `rctx_...` IDs).
- `BlockedReportID` / `BlockedScope` / `BlockedScopeID` / `BlockedReason` --
  BLOCKED-report correlation.

These are reused as-is; #765 does not need to redefine delivery, read/claim,
reply-slot-fill, or BLOCKED-report correlation, which are already distinct
fields with distinct owners (#765's "keep delivery, read/claim, reply-slot
fill, substantive review, and final task acceptance separate" requirement is
already partially satisfied structurally: `ReplyTo` is transport-level
reply-slot correlation; it is not, and is not used as, a task-acceptance
signal anywhere inspected in this pass).

## 2. The gap: no cross-session origin reference

`ContextID` and `ReplyTo` are both scoped to **one session's own mailbox**:
a session's `context_id` identifies messages exchanged among the nodes of
*that* tmux session, and `ReplyTo` only ever points to a message ID that
exists in the same mailbox. When a top-level mouthpiece's orchestrator
delegates into a *child* session (a different tmux session, with its own
independent `context_id` sequence), the first message of that delegation
starts a brand-new `ContextID` with no field linking it back to the
`ContextID`/`MessageID` of the original top-level request that caused it.

Concretely, for the 3-level example in #765's own issue body (top-level ->
owner/group -> repository), each hop's messages are fully traceable
*within* that hop's own session via `ContextID`/`ReplyTo`, but nothing in
`Metadata` says "this repository-session exchange exists because of
top-level message X in context Y" -- that linkage currently only lives in
prose (a human-written task description), not in a durable, inspectable
field. This is exactly the gap #765 names: "Define a durable mapping
between an incoming request and the outgoing relay/delegation exchanges it
causes. Preserve the original request ... through references."

## 3. Recommended minimal addition (not implemented in this slice)

Two new optional `Metadata` fields, additive and backward-compatible
(absent on every existing archived message, which must continue to parse
unchanged):

- `OriginContextID` -- the `ContextID` of the message that started the
  whole multi-hop chain (set once, at the first hop that crosses a session
  boundary; carried forward unchanged by every subsequent hop's outgoing
  delegation message).
- `OriginMessageID` -- the specific `MessageID` of that originating request,
  for an exact pointer back to the first durable record (not just "some
  message in that context").

Propagation rule (for the next slice to implement, not this one): when an
orchestrator's outgoing delegation message is itself caused by an incoming
request, the new message's `OriginContextID`/`OriginMessageID` equal the
incoming request's own `OriginContextID`/`OriginMessageID` if already set
(preserve the root), or its `ContextID`/`MessageID` if this is the first
hop (establish the root). This mirrors #765's own non-goal boundary: no new
task database or planner is needed, only two carried-forward pointer
fields.

## 4. Why this slice does not implement the fields yet

`Metadata`'s read side (`ParseMetadata`, `metadata.go:212+`), the
allowed-key copy-forward list (`metadata.go:501`), the canonical-key mapper
(`metadata.go:648+`), and the alias-key function (`metadata.go:715+`) are
four separate touch points that must all agree for a new field to parse,
copy forward through reply chains, and round-trip through both
`camelCase`/`snake_case` spellings consistently with every existing field.
Implementing and verifying all four correctly, plus the actual
orchestrator-side propagation logic and a genuine 3-level fixture, is
properly its own reviewable slice (see Deliverables below) rather than a
rushed addition to this already wide-reaching, shared parsing code.

## 5. Next slice (not this one)

1. Add `OriginContextID`/`OriginMessageID` to `Metadata` and wire all four
   parse/copy/canonicalize/alias touch points above.
2. A focused fixture: three synthetic messages across three distinct
   `ContextID`s (simulating top-level -> owner/group -> repository), proving
   `OriginContextID`/`OriginMessageID` survive unchanged through both hops
   while `ContextID`/`ReplyTo` correctly vary per hop.
3. Wire actual propagation into the orchestrator's outgoing delegation path
   (not located/scoped in this pass).
4. Address #765's remaining acceptance criteria (loop detection, concurrent
   same-named-mouthpiece isolation, restart/timeout/replay correlation,
   authorization-per-hop) as further slices; none of those are claimed
   solved by this inventory.

## 6. Explicit non-claims

- This slice adds no code and changes no runtime behavior.
- It does not close any #765 acceptance-criteria checkbox.
- The field names (`OriginContextID`/`OriginMessageID`) are a recommendation
  from this inventory, not a locked-in decision; naming and propagation
  semantics should be reviewed before the next slice implements them.
