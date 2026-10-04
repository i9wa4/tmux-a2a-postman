# #767: external-channel contract for decoupling human interaction from tmux

Documentation-only design artifact. No production code in this PR. This document
reuses the Guardian-APPROVED content from the prior #767 reassessment artifact
(`~/.local/state/mkmd/i9wa4-tmux-a2a-postman/2026-10-04-main/plans/issue767-channel-reassessment-9mTxQw.md`,
approved 2026-10-04 14:14 JST) rather than restating it from scratch.

## 1. Scope

Defines the channel-independent contract #767 requires before any
external (e.g. voice-first) channel can connect to the top-level
mouthpiece node that #764 (mouthpiece unification, currently OPEN, not
yet implemented) will introduce. This is a protocol-shape sketch, not a
specification ready to implement, and it depends on #764/#765/#766
landing first (see "Remaining dependencies" below).

## 2. Historical context (corrected, not the original false claim)

An earlier draft of this reassessment incorrectly claimed the #298-family
phony-node/binding/sidecar design "does not exist anywhere in the current
repo" and "was never implemented." Guardian's design review found this
false. The corrected, git-verified history:

The B/C/D/E implementation chain **was built and merged** across 8 PRs,
then **later removed** as part of an unrelated CLI-scope-reduction
cleanup:

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
against the security requirements (S-1 through S-4) below before any
reuse.

**What remains in the current tree today**, independently of the above:
only node-name regex validation and VT/control-sequence stripping
(`StripVT`, `internal/notification/notification.go`,
`internal/cli/send_message.go`). `IsPhony`, `BindingRegistry`,
`DeliverToPhonyNode`, a `--from` flag, and any `supervisor` package are
**not present in the current tree**, recoverable from history as noted
above, not something to build from first principles.

## 3. Four entities (abstract contract, channel-independent)

1. **Durable exchange ID** -- reuses #765's own planned correlation-token
   mechanism (#765 is OPEN, not yet implemented; this contract depends on
   it, it does not itself build it). One exchange ID spans the full
   lifecycle of one human-originated request: inbound request, zero or
   more pending-decision round-trips, and the final outbound report.

2. **Channel/user provenance** -- kept strictly separate from the
   existing role/parent relay identity. A request's provenance (which
   external channel, which human user on that channel) is metadata
   attached to the exchange, never inferred from or conflated with which
   tmux-side role happens to be relaying it at a given hop.

3. **Decision binding** -- a tuple of `{exchange_id, action scope/revision
   digest, approval-context snapshot}`. A decision presented back into
   the system MUST match all three fields against what was originally
   sent out for decision; a mismatch on any field is an outright
   rejection, never a best-effort match.

4. **Delivery acknowledgement vs. task acknowledgement** -- these are two
   distinct, never-conflated acknowledgements:
   - *Delivery acknowledgement*: the external channel confirms it
     received/delivered a message (a transport-layer fact).
   - *Task acknowledgement*: the system confirms a request has actually
     been accepted for processing, or a human has actually acted on a
     report (a work-layer fact).
   Conflating the two would let "the message was handed to the channel"
   be mistaken for "a human saw and acted on it."

## 4. Four flows

### 4.1. Inbound request (external channel to node)

Carries an idempotency key scoped to the (channel, external conversation)
pair, a channel-side timestamp (advisory only -- the node's own receipt
time is canonical), the raw channel-native payload, enough provenance to
address a later outbound report back to the same place, and exactly one
resolved target node identity. Delivery is at-least-once, idempotent on
the key.

- **S-1 (HIGH, security)**: receipt MUST authenticate the external
  principal/source AND check an ACTIVE binding to the target
  session/node at that exact moment, with revocation taking effect
  immediately on rebind or teardown. Resolving an address, or observing
  that a session is merely enabled, is NOT source binding (the #700
  M2-B1 lesson).
- **S-2 (HIGH, security)**: the raw channel-native payload MUST be
  sanitized (VT/control-sequence stripping, invalid-UTF-8 handling)
  BEFORE any pane or tmux notification is attempted, reusing the
  already-implemented `StripVT` machinery. An external channel's payload
  is attacker-influenced input exactly as #845's fixture PaneIDs were not
  meant to be real targets.
- **S-4 (MEDIUM, security/QA)**: idempotency-key retention duration MUST
  be at least the bounded catch-up window's own retention threshold
  (A-2) -- a key retained for less time than the catch-up window would
  let a legitimately-delayed redelivery be misread as new.

### 4.2. Outbound report (node to external channel)

Carries target channel/conversation identity, a report body, and a
report "kind" tag so the channel-specific adapter decides rendering. Not
assumed synchronous or guaranteed; failures are classified as
transient/redeliverable vs. terminal (reusing #309's reason taxonomy as
precedent). Reports for the same conversation carry a monotonic
per-binding sequence number, not wall-clock time.

### 4.3. Pending-decision response

The node must be able to return an intermediate "acknowledged, pending"
response, distinct from a final answer. A pending response carries a
correlation token; the eventual final response must reference the same
token. Status must be independently pollable, not only push-delivered.

- **S-3 (HIGH, security)**: holding a correlation token must NOT by
  itself confer authority to act. A status-poll or answer-submission
  request MUST additionally be authorized against the caller's own
  binding/conversation scope, an expiry, and a supersede-on-rebind rule
  (A-3). An answer MUST also be validated against the pending action's
  current revision/digest, not merely its token.

### 4.4. Reconnect and catch-up semantics

- **A-1 (HIGH, architecture)**: the stream referent must be ONE of: one
  durable event stream per (binding, epoch) with cursor N indexing the
  full stream across every conversation that pair has seen; or a
  per-conversation stream, with cursor N scoped to one conversation.
  **This design explicitly DEFERS the choice to #764/#765**, since
  picking one requires knowing those issues' own target-identity and
  exchange-provenance shape first.
- Catch-up is expressed as "everything since cursor/sequence N," never
  "everything since timestamp T" (clock drift makes timestamp-based
  catch-up unreliable).
- **A-2 (HIGH, architecture)**: bounded catch-up must concretely define
  a retention threshold; an explicit cursor-too-old-or-gap result
  distinct from a normal catch-up response; a reconciliation snapshot of
  current binding state and pending decisions issued alongside that
  result; and a safe resume cursor handed back in the snapshot.
- **A-3 (MEDIUM, architecture)**: a binding must carry an explicit
  generation/epoch number, incremented on every rebind. A cursor issued
  under a prior epoch must not be silently accepted against the current
  epoch's stream, which is what makes S-3's supersede-on-rebind rule
  enforceable.

## 5. Negative outcomes (Q-2)

1. **Inbound request**: a forged, misbound, or changed-payload duplicate
   of an already-processed idempotency key must be distinguishable from a
   legitimate at-least-once redelivery via an explicit rejection reason,
   never a silent no-op.
2. **Outbound report**: delivery to an unauthorized report target must be
   refused with an explicit reason, never silently dropped or
   redirected; the delivery-acknowledgement/task-acknowledgement
   distinction above must be preserved here specifically.
3. **Pending-decision response**: a DENIED decision, an EXPIRED token, a
   SUPERSEDED decision (per A-3's epoch rule), and a lost completion push
   are four distinct negative outcomes, not one generic failure.
4. **Reconnect and catch-up**: a stale cursor (A-2) and a cursor from a
   prior epoch presented against the current epoch (A-3) are different
   failure modes requiring different adapter behavior. A rebind
   occurring while a catch-up is in progress is a fifth case this design
   has not yet resolved and is explicitly left unresolved, not silently
   assumed safe.

## 6. Remaining dependencies

- The node abstraction channel adapters bind against: #298's
  `Plugin{Poll/Ack/Send}` was implemented and is recoverable via
  `git show feff0ab^:internal/plugin/plugin.go`, but predates S-1/S-2/S-3
  and needs re-review against them before reuse.
- Durable storage for the per-binding sequence counter and idempotency
  keys: #307/#309's `internal/memory` store is similarly recoverable and
  stale, not a from-scratch design question.
- Exact wire/serialization format for all four message shapes (this
  design fixes required fields and semantics, not an encoding).
- Depends on #764 (OPEN) for the target identity (the mouthpiece or
  successor node) this design addresses requests/reports to, and for
  resolving A-1's deferred stream-referent choice; #765 (OPEN) for
  exchange provenance and return-path linkage, and the other half of
  A-1's deferred decision; #766 (OPEN) for a canonical status/decision
  vocabulary this design's pending-decision response should align with;
  and, not covered by any of #764/#765/#766, a durable event/outbox/ack
  mechanism, a retention policy, and a binding-identity scheme, none of
  which exist yet in any form.

## 7. Stub fixture now versus real integration later

A near-term, deterministic test fixture can exercise this contract's
field shapes and flow sequencing entirely in-process using plain structs
for the four entities and function calls for the four flows, without any
real external channel, voice interface, or network transport. Real
integration is explicitly deferred until #764, #765, and #766 land.

## 8. Acceptance checklist (this artifact's own scope)

- [x] Four entities defined with exact field shapes, matching the
      Guardian-approved research artifact.
- [x] Four flows defined with exchange-ID continuity, S-1 through S-4 and
      A-1 through A-3 security/architecture requirements, and ack
      separation.
- [x] Historical accuracy: #302-309 implemented and merged, then removed
      by `feff0ab`, not "never implemented"; only node validation and
      `StripVT` remain in the current tree today.
- [x] #765's correlation mechanism described as a dependency (OPEN, not
      yet implemented), not as something already available.
- [x] In/out boundary stated; stub-fixture-vs-future-integration
      distinction stated.
- [x] Negative-outcome scenarios (Q-2) for all four flows.
- [ ] Guardian review of this PR-committed derivative (not yet
      requested as of this commit; the content itself was already
      approved in its prior research-artifact form).
