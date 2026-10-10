# #767: external-channel contract for decoupling human interaction from tmux

Documentation-only design artifact. No production code in this PR.
Defines the channel-independent contract #767 requires before any
external (e.g. voice-first) channel can connect to the node designated by
the configured canonical `interface_node` (#764). This is a contract
sketch, not a wire specification: it fixes entities, required fields,
authorization rules, and outcomes, and it leaves the encoding and the A-1
stream choice open (see "Remaining dependencies" below).

Dependency status at the time of writing: #764's first slice (the TOML
`interface_node` path) merged via #852; its slice A (explicit
`interface_node`, `ui_node` alias removal) is under review and not merged.
For #765 and #766, first-slice inventories merged (#854, #855); their
runtime work has not landed. The contract is written before
implementation, and it does not depend on any of those pieces being
finished.

## 1. Historical context

The #298-family phony-node/binding/sidecar design was not "never
implemented". The B/C/D/E implementation chain **was built and merged**
across 8 PRs, then **later removed** as part of an unrelated
CLI-scope-reduction cleanup:

| Label | Issue | PR   | Commit    | Subject                                                                    |
| ----- | ----- | ---- | --------- | -------------------------------------------------------------------------- |
| B-1   | #302  | #329 | `8c9d784` | feat(discovery): extend NodeInfo with IsPhony field                        |
| B-2   | #303  | #330 | `8b47bae` | feat(binding): implement full BindingRegistry loader with validation       |
| C-2   | #304  | #334 | `5cc5c92` | feat(main): add --from flag to runCreateDraft with phony-node inbound auth |
| C-1   | #305  | #333 | `2337a09` | feat(message): implement DeliverToPhonyNode handler                        |
| D-1   | #306  | #335 | `8b644e9` | feat(message): add IsPhony dispatch branch in DeliverMessage               |
| E-1   | #308  | #336 | `1b8a852` | feat(deploy): implement Phase 1 phony deployment direct binding            |
| E-2   | #307  | #337 | `3208118` | feat(supervisor): deploy supervisor/memory/pilot-gate                      |
| E-3   | #309  | #338 | `985367a` | feat(supervisor): Phase 3 autonomous operation with escalation             |

Removal commit `feff0ab` ("chore: remove non-core legacy surfaces",
2026-05-03) deleted `internal/plugin/`, `internal/memory/`,
`internal/supervisor/`, `internal/bindcmd/`, `e2e/phony_test.go`, and
stripped `internal/binding/binding.go` / `internal/message/message.go`
back to their non-phony contents, as part of a broader "reduce to core
commands" cleanup (traced to issue #362), not a defect-driven revert of
the phony-node design itself. The removed code is recoverable via
`git show feff0ab^:<path>` and is a genuine reuse candidate (not dead
boilerplate), though roughly five months stale against current
`internal/message`/`internal/discovery` APIs and in need of re-review
against the security requirements (S-1 through S-5) below before any
reuse.

**What remains in the current tree today**, independently of the above:
only node-name regex validation and VT/control-sequence stripping
(`StripVT`, `internal/notification/notification.go:360-363,438-451`,
`internal/cli/send_message.go:419`). `IsPhony`, `BindingRegistry`,
`DeliverToPhonyNode`, a `--from` flag, and any `supervisor` package are
**not present in the current tree**, recoverable from history as noted
above, not something to build from first principles.

## 2. Referent table

The contract below involves several distinct, easily-conflated
identifiers. This table is the single source of truth for each:

| Referent                           | Created by                                                                                              | Carried in                                                                                                                                                                                                                                                | Scope                                                                              | Invalidated by                               |
| ---------------------------------- | ------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- | -------------------------------------------- |
| Exchange ID                        | Node, when it ACCEPTS the first inbound request (returned in the inbound acceptance acknowledgement)    | Inbound acceptance acknowledgement; every later channel-to-node message for the exchange (status poll, answer submission); outbound report; pending and final responses; catch-up entries replaying exchange-linked events (lifecycle entries carry none) | One human-originated request's full lifecycle                                      | Never reused; a new exchange gets a new ID   |
| Pending-decision token             | Node, when it emits an "acknowledged, pending" response                                                 | Issued in the pending response; presented on status poll and on answer submission; referenced by the final response. Maps back to exactly one exchange ID                                                                                                 | One pending decision instance within one exchange                                  | Expiry, or a rebind (epoch bump) per A-3     |
| Report sequence / catch-up cursor  | Node, as one referent whose scope is the A-1 stream (binding-wide or per-conversation; A-1 is deferred) | Outbound report (sequence number); reconnect/catch-up (cursor)                                                                                                                                                                                            | The A-1 stream within one (binding, epoch) pair, identical in both uses -- see A-1 | A rebind (epoch bump) per A-3                |
| Delivery acknowledgement           | External channel                                                                                        | N/A (channel-to-node signal, not part of the node-authored flows)                                                                                                                                                                                         | One delivery attempt                                                               | N/A -- a transport-layer fact, not revocable |
| Inbound acceptance acknowledgement | Node                                                                                                    | Response to an inbound request (returns the minted exchange ID) and to an answer submission (accepted, or rejected with a reason)                                                                                                                         | One inbound request or one answer submission                                       | N/A -- a work-layer fact once given          |
| Report-action acknowledgement      | External channel / human                                                                                | Implicit response to outbound report                                                                                                                                                                                                                      | One outbound report                                                                | N/A -- a work-layer fact once given          |

## 3. Four entities (abstract contract, channel-independent)

1. **Durable exchange ID** -- the exchange ID is the node's own durable
   identifier for one request's full lifecycle (see referent table). The node
   mints it when it accepts the first inbound request; the raw inbound request
   itself carries an idempotency key, not an exchange ID. It is expected to be
   persisted and correlated using whatever mechanism the #765 work (runtime
   provenance, not yet implemented) defines for hierarchical request/return
   provenance; this contract depends on that mechanism existing, it does not
   itself build or presume its exact shape beyond "a durable, correlatable
   identifier." One exchange ID spans the full lifecycle of one human-originated
   request: inbound request, zero or more pending-decision round-trips, and the
   final outbound report.

2. **Channel/user provenance** -- kept strictly separate from the
   existing role/parent relay identity. A request's provenance (which
   external channel, which human user on that channel) is metadata
   attached to the exchange, never inferred from or conflated with which
   tmux-side role happens to be relaying it at a given hop.

3. **Decision binding** -- a tuple of `{exchange_id, action scope/revision
   digest, approval-context snapshot}`. Every authorization challenge in
   this contract (S-3's status-poll/answer-submission check, below) is
   bound to this exact tuple: a request presenting only a valid token but
   failing to also match the current action scope/revision digest and
   approval-context snapshot is rejected, never accepted on token
   validity alone. A decision presented back into the system MUST match
   all three fields against what was originally sent out for decision; a
   mismatch on any field is an outright rejection, never a best-effort
   match.

4. **Three distinct acknowledgements, never conflated** (see referent
   table):
   - _Delivery acknowledgement_: the external channel confirms it
     received/delivered a message (a transport-layer fact, not
     node-authored).
   - _Inbound acceptance acknowledgement_: the node confirms an inbound
     item -- a new request (4.1) or an answer submission (4.3) -- has
     actually been accepted for processing (a work-layer fact, distinct
     from mere delivery).
   - _Report-action acknowledgement_: a human has actually acted on an
     outbound report (a work-layer fact, distinct from both of the
     above -- a report being delivered does not mean a human acted on
     it).

   Conflating any of the three would let "the message was handed to the
   channel" be mistaken for "the system accepted the task" or "a human
   saw and acted on it."

## 4. Four flows

The pending-decision flow (4.3) has two halves: the node-to-channel
pending and final responses, and the channel-to-node status poll and
answer submission. Authorization of every channel-to-node access after the
first inbound request is stated once, in 4.5 (S-5).

### 4.1. Inbound request (external channel to node)

Carries: an idempotency key scoped to the (channel, external
conversation) pair; a channel-side timestamp (advisory only -- the node's
own receipt time is canonical); the raw channel-native payload; enough
provenance to address a later outbound report back to the same place; and
exactly one resolved target node identity. It does not carry an exchange
ID: the node mints one when it accepts the request and returns it in the
inbound acceptance acknowledgement. Delivery is at-least-once, idempotent
on the key, so a redelivered duplicate is answered with the same minted
exchange ID rather than a new one.

The target is the node designated by the configured canonical
`interface_node` (#764). An external binding REQUIRES that setting: when
it is omitted the binding fails closed, and `ui_node` is not accepted as
an alias.

- **S-1 (HIGH, security)**: receipt MUST authenticate the external
  principal/source AND check an ACTIVE binding to the target session/node
  at that exact moment, with revocation taking effect immediately on
  rebind or teardown. Resolving an address, or observing that a session is
  merely enabled, is NOT source binding -- this is the M2-B1 lesson from
  PR #700 (an address that resolves to a target is a routing fact, not
  proof the claimed sender is who a currently valid binding says may act
  as that sender).
- **S-2 (HIGH, security)**: the raw channel-native payload MUST be
  sanitized (VT/control-sequence stripping, invalid-UTF-8 handling)
  BEFORE any pane or tmux notification is attempted, reusing the
  already-implemented `StripVT` machinery
  (`internal/notification/notification.go:360-363,438-451`,
  `internal/cli/send_message.go:419`). An external channel's payload is
  attacker-influenced input exactly as #845's fixture PaneIDs were not
  meant to be real targets.
- **S-4 (MEDIUM, security/QA)**: idempotency-key retention duration MUST
  be at least `max(ordinary redelivery delay, bounded catch-up window)`
  (catch-up window per A-2) -- a key retained for less time than either
  the longest expected ordinary redelivery delay or the catch-up window
  would let a legitimately-delayed redelivery, or a catch-up replay, be
  misread as a new, non-duplicate event.

### 4.2. Outbound report (node to external channel)

Carries: the exchange ID; target channel/conversation identity; a report
body; and a report "kind" tag so the channel-specific adapter decides
rendering. Not assumed synchronous or guaranteed. Each report carries a
monotonic sequence number within the A-1 stream it belongs to, not
wall-clock time. This sequence number uses the SAME stream referent as the
reconnect/catch-up cursor (A-1); they are the same counter viewed from two
flows, not two independent numbering schemes. Whether that stream is
binding-wide or per-conversation is the A-1 choice, which this design
defers.

Failures are classified as transient/redeliverable (e.g.
`session_offline`, `channel_unbound`, `sidecar_unavailable`) vs. terminal
(e.g. `routing_denied`, `redelivery_failed`, missing idempotency key),
reusing #309's reason taxonomy as precedent. A transient reason is
retried, bounded by normal redelivery policy; a TERMINAL reason MUST go to
dead-letter/quarantine and MUST NOT be retried forever -- an adapter or
node that keeps retrying a terminal-classified failure indefinitely
violates this contract.

### 4.3. Pending-decision response and answer submission

The node must be able to return an intermediate "acknowledged, pending"
response, distinct from a final answer. A pending response carries the
exchange ID plus a separate pending-decision token (see referent table:
these are two different identifiers -- the exchange ID identifies the
whole request's lifecycle, the pending-decision token identifies this one
specific pending-decision instance within it, and maps back to exactly one
exchange ID). Token carriage, end to end: the node issues the token in the
pending response; the channel presents it on a status poll and on an
answer submission; the eventual final response references the same token.
Status must be independently pollable, not only push-delivered.

**Answer submission (channel to node).** An answer carries the exchange
ID, the pending-decision token, the entity 3 action scope/revision digest
and approval-context snapshot, and an idempotency key (scoped as in 4.1).
The node returns an explicit result: accepted, or rejected with a reason.
That result is an instance of the inbound acceptance acknowledgement, not
a fourth acknowledgement kind. A repeat of the same key with the same
payload is idempotent: same result, no second side effect. A repeat of the
same key with a changed payload is rejected with an explicit reason
(mirroring negative outcome 1 in section 5). Rejection reasons are the
three decision outcomes of section 5 item 3 (denied, expired, superseded),
an entity 3 mismatch, and the S-5 refusals (unauthorized, no active
binding). This design fixes the required fields and semantics, not an
encoding.

- **S-3 (HIGH, security)**: holding a pending-decision token must NOT by
  itself confer authority to act. A status-poll or answer-submission
  request MUST additionally be authorized against entity 3's full
  `{exchange_id, action scope/revision digest, approval-context
  snapshot}` tuple -- the caller's own binding/conversation scope, an
  expiry, and a supersede-on-rebind rule (A-3) -- never against token
  possession alone, and under the S-5 authentication and active-binding
  checks. An answer MUST also be validated against the pending action's
  current revision/digest, not merely its token: if the underlying pending
  action changed between issuance and answer, a stale answer matching only
  the token is rejected, not silently applied.

### 4.4. Reconnect and catch-up semantics

- **A-1 (HIGH, architecture)**: the stream referent must be ONE of: one
  durable event stream per (binding, epoch) with cursor N indexing the
  full stream across every conversation that pair has seen; or a
  per-conversation stream, with cursor N scoped to one conversation and an
  explicit lifecycle consequence defined for what happens to a
  conversation's stream when its binding tears down or rebinds (a
  per-conversation stream has no natural single point, unlike the
  binding-wide option, where that lifecycle event is just another stream
  entry). **This design explicitly DEFERS the choice to #764/#765**, since
  picking one requires knowing those issues' own target-identity and
  exchange-provenance shape first. Whichever option is chosen, this design
  requires the INVARIANT that the outbound report's sequence number
  (above) and this section's catch-up cursor use the exact SAME referent
  -- they are never allowed to diverge into two separately-numbered
  schemes.
- Catch-up is expressed as "everything since cursor/sequence N," never
  "everything since timestamp T" (clock drift makes timestamp-based
  catch-up unreliable). Catch-up MUST be idempotent: replaying an
  already-seen cursor range is safe and produces no duplicate side
  effects. Catch-up entries that replay exchange-linked events (outbound
  reports, pending and final decision results) carry that event's
  exchange ID; binding lifecycle entries (teardown, rebind, epoch change)
  carry none.
- **A-2 (HIGH, architecture)**: bounded catch-up must concretely define a
  retention threshold; an explicit cursor-too-old-or-gap result distinct
  from a normal catch-up response; a reconciliation snapshot of current
  binding state and pending decisions issued alongside that result; and a
  safe resume cursor handed back in the snapshot. The catch-up request and
  the snapshot are both subject to S-5.
- Binding lifecycle changes (teardown, rebinding) MUST be visible through
  this same catch-up stream, not a separate side channel -- otherwise a
  reconnecting adapter can miss exactly the event (a rebind) that would
  tell it where to resume.
- **A-3 (MEDIUM, architecture)**: a binding must carry an explicit
  generation/epoch number, incremented on every rebind. BOTH the
  pending-decision token (entity 3 / S-3) AND the catch-up cursor (above)
  are scoped to a specific (binding, epoch) pair, and a single rebind's
  epoch bump is the ONE mechanism that invalidates both at once -- never
  two separate invalidation paths that could drift out of sync with each
  other. A token or cursor issued under a prior epoch must not be silently
  accepted against the current epoch's stream.

### 4.5. Authorization of post-inbound access

- **S-5 (HIGH, security)**: every channel-to-node access after the first
  inbound request -- status poll, answer submission, catch-up, and the A-2
  reconciliation snapshot of binding state and pending decisions -- MUST
  authenticate the external principal AND check an ACTIVE binding to the
  target at that moment. Between a teardown and a later rebind there is no
  active binding, so no such access is served: it is refused with an
  explicit reason, never answered from the prior epoch's state. A cursor,
  a pending-decision token, and an epoch are positions or referents, not
  proof of who is asking, and they are never accepted as authentication.
  Beyond authentication and the active-binding check, the scope rule is:
  - status poll and answer submission are additionally authorized against
    the caller's conversation scope and entity 3's tuple (S-3);
  - catch-up and the snapshot are authorized at the same scope as the
    stream chosen under A-1: if the stream is binding-wide, the principal
    must be authorized for that binding; if it is per-conversation, for
    that conversation. This rule is stated conditionally and does not
    resolve A-1.

## 5. Negative outcomes (Q-2)

1. **Inbound request**: a forged, misbound, or changed-payload duplicate
   of an already-processed idempotency key must be distinguishable from a
   legitimate at-least-once redelivery via an explicit rejection reason,
   never a silent no-op.
2. **Outbound report**: delivery to an unauthorized report target must be
   refused with an explicit reason, never silently dropped or redirected;
   the delivery/inbound-acceptance/report-action acknowledgement
   distinction above must be preserved here specifically.
3. **Pending-decision response**: these are three distinct DECISION
   outcomes, not one generic failure -- a DENIED decision, an EXPIRED
   token, and a SUPERSEDED decision (per A-3's epoch rule) each need their
   own explicit, observable result. An access refused for lack of an
   authenticated principal or an active binding (S-5) also gets its own
   explicit reason; it is not a decision outcome.
4. **Lost completion push** is a categorically SEPARATE, delivery-layer
   failure, not a fourth decision outcome -- per the "status must be
   independently pollable" requirement above, a lost push must be
   recoverable by polling, distinguishing "we don't know what happened,
   poll for the answer" from any of the three decision outcomes in (3).
5. **Reconnect and catch-up**: a stale cursor (A-2) and a cursor from a
   prior epoch presented against the current epoch (A-3) are different
   failure modes requiring different adapter behavior. A rebind occurring
   while a catch-up is in progress is covered by neither; this design has
   not resolved it and leaves it explicitly unresolved, not silently
   assumed safe.

## 6. Remaining dependencies

- The node abstraction channel adapters bind against: #298's
  `Plugin{Poll/Ack/Send}` was implemented and is recoverable via
  `git show feff0ab^:internal/plugin/plugin.go`, but predates
  S-1/S-2/S-3/S-5 and needs re-review against them before reuse.
- Durable storage for the report-sequence/catch-up-cursor stream (whose
  scope is the A-1 choice) and for idempotency keys: #307/#309's
  `internal/memory` store is similarly recoverable and stale, not a
  from-scratch design question. Separately, this repository already has a
  durable, fsynced event-journal mechanism (`internal/journal`) in active
  use elsewhere; no channel-specific event/outbox/ack mechanism exists
  yet, but the existing journal infrastructure is a plausible foundation
  to build this contract's durable stream on, not a from-nothing design
  question either.
- Exact wire/serialization format for all message shapes (this design
  fixes required fields and semantics, not an encoding).
- Depends on #764 for the target identity (the configured canonical
  `interface_node`) this design addresses requests/reports to, and for
  resolving A-1's deferred stream-referent choice. Its first slice merged
  via #852; slice A (explicit `interface_node`, `ui_node` alias removal)
  is under review and not merged. Depends on #765 for exchange provenance
  and return-path linkage, and the other half of A-1's deferred decision;
  and on #766 for a canonical status/decision vocabulary this design's
  pending-decision response should align with. The #765 and #766
  first-slice inventories merged (#854, #855); their runtime work has not
  landed. A retention policy and binding-identity scheme do not exist yet
  in any form.
- Deferred, not specified by this artifact: how an adapter that holds a
  prior-epoch cursor recovers (only the A-2 cursor-too-old-or-gap result
  and snapshot are defined); the shape of an inbound request after a
  reconnect; and a complete reason taxonomy (4.2 cites #309's taxonomy as
  precedent only).

## 7. Stub fixture now versus real integration later

A near-term, deterministic test fixture can exercise this contract's
field shapes and flow sequencing entirely in-process using plain structs
for the four entities and function calls for the four flows, without any
real external channel, voice interface, or network transport. Real
integration is explicitly deferred until the runtime work of #764, #765,
and #766 lands.

## 8. Acceptance checklist (this artifact's own scope)

- [x] Four entities defined, including the referent table distinguishing
      exchange ID, pending-decision token, and report-sequence/catch-up
      cursor (stream scope conditional on the deferred A-1 choice).
- [x] Four flows defined, with the exchange ID minted at inbound
      acceptance and carried as the referent table states, S-1 through
      S-5 and A-1 through A-3 security/architecture requirements, and the
      three-way acknowledgement split.
- [x] Post-inbound access authorization (S-5) stated for status poll,
      answer submission, catch-up, and the reconciliation snapshot, with
      the catch-up scope conditional on A-1.
- [x] Answer submission fields, idempotency, and accept/reject result
      stated as the channel-to-node half of the pending-decision flow.
- [x] Historical accuracy: #302-309 implemented and merged, then removed
      by `feff0ab`, not "never implemented"; only node validation and
      `StripVT` remain in the current tree today (exact source
      coordinates cited).
- [x] #765's correlation mechanism described narrowly, as a dependency
      (runtime work not yet landed) this contract's exchange ID expects to
      use, not as an existing, specific token format.
- [x] In/out boundary stated; stub-fixture-vs-future-integration
      distinction stated.
- [x] Negative-outcome scenarios (Q-2) for all four flows, with the lost
      completion push kept categorically distinct from the three decision
      outcomes.
- [x] No machine-local filesystem paths.
- [ ] Not closed by this artifact (explicitly deferred): the A-1 stream
      scope, a rebind during an in-progress catch-up, the wire encoding,
      prior-epoch cursor recovery, the inbound shape after a reconnect,
      and a complete reason taxonomy.
