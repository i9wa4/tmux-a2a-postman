# #767: external-channel contract for decoupling human interaction from tmux

Documentation-only design artifact. No production code in this PR.

## 1. Scope

Defines the channel-independent contract #767 requires before any
external (e.g. voice-first) channel can connect to the top-level
mouthpiece, per issue #767. This artifact is a stub-fixture-level
contract: it specifies field shapes and flow sequencing so that a future
real integration (after #764/#765/#766/#767's own dependent issues land)
has a fixed target, not a moving one.

## 2. Four entities

1. **Durable exchange ID** -- reuses #765's own correlation-token
   mechanism rather than inventing a parallel ID space. One exchange ID
   spans the full lifecycle of one human-originated request: inbound
   request, zero or more pending-decision round-trips, and the final
   outbound report. Scoped and expired the same way #765's correlation
   tokens are (not re-specified here; this artifact only asserts reuse,
   not redefinition).

2. **Channel/user provenance** -- kept strictly separate from the
   existing role/parent relay identity. A request's provenance (which
   external channel, which human user on that channel) is metadata
   attached to the exchange, never inferred from or conflated with which
   tmux-side role (mouthpiece/orchestrator/worker/etc.) happens to be
   relaying it at a given hop. This separation is what lets the same
   relay chain serve multiple channels/users without cross-talk.

3. **Decision binding** -- a tuple of `{exchange_id, action scope/revision
   digest, approval-context snapshot}`. Any decision (approve/reject/
   other) presented back into the system MUST match all three fields
   against what was originally sent out for decision; a mismatch on any
   field is an outright rejection, never a best-effort match. This
   directly prevents a stale or cross-exchange decision from being
   silently applied to the wrong action.

4. **Channel-delivery-ack vs task-acceptance-ack** -- these are two
   distinct acknowledgements and must never be conflated:
   - *Channel-delivery-ack*: the external channel confirms it has
     delivered a message/prompt to the human (a transport-layer fact).
   - *Task-acceptance-ack*: the system confirms a request has actually
     been accepted for processing (a work-layer fact).
   A channel-delivery-ack must never be read as implying task acceptance,
   and vice versa.

## 3. Four flows

1. **Inbound request**: human -> external channel -> top-level
   mouthpiece. Mints a new exchange ID (entity 1), attaches channel/user
   provenance (entity 2), and emits a channel-delivery-ack once the
   channel confirms the human saw the prompt -- separate from whatever
   task-acceptance-ack follows once the mouthpiece actually begins
   routing the request.

2. **Outbound report**: top-level mouthpiece -> external channel ->
   human. Carries the same exchange ID throughout; the channel's
   delivery-ack for the report is, again, independent of whether the
   underlying task is considered complete by the system.

3. **Pending-decision response**: a decision travels back in using the
   decision-binding tuple (entity 3). Any mismatch against the
   originally-sent `{exchange_id, action scope/revision digest,
   approval-context snapshot}` is rejected outright, with the rejection
   reported back through the channel, not silently dropped.

4. **Reconnect/catch-up without re-trigger**: a human reconnecting after
   a disconnect must be able to receive their exchange's current state
   (including any already-sent pending-decision prompt) without
   re-triggering the underlying action a second time. This flow is
   read-only with respect to action execution; it only ever replays
   already-emitted outbound content for exchanges that are still open.

## 4. Current-state assessment (honesty, not aspiration)

As of this artifact (2026-10-05), only the following has actually landed
from the earlier #298/#307-309 effort:

- Node-name regex validation.
- VT (control-sequence) stripping on notification payloads.

The following do **not** exist in the codebase today and must not be assumed
present by any future implementation reading this artifact: `IsPhony`,
`BindingRegistry`, `DeliverToPhonyNode`, a `--from` flag on any relevant
command, and any `supervisor` package implementing phony-node routing. (See the
separate #767 reassessment artifact,
`~/.local/state/mkmd/i9wa4-tmux-a2a-postman/2026-10-04-main/plans/issue767-channel-reassessment-9mTxQw.md`,
for the full provenance trace of what was built, then removed, and why.)

## 5. In/out boundary for this artifact

**In scope**: the four entities and four flows above, as field shapes and
sequencing only; the stub-fixture-vs-future-integration distinction below.

**Out of scope**: any production code implementing these entities/flows;
the actual external-channel transport (voice, chat, etc.); #764/#765/#766's
own unimplemented mouthpiece-unification, provenance-preservation, and
status-visibility work, which this contract depends on but does not
itself build.

## 6. Stub fixture now vs. real integration later

A near-term, deterministic test fixture can exercise this contract's
field shapes and flow sequencing entirely in-process (matching #768's own
"use normal producer/consumer interfaces, never a synthetic shortcut"
constraint) using plain structs for the four entities and function calls
for the four flows, without any real external channel, voice interface,
or network transport. Real integration -- an actual external channel
connecting to a real top-level mouthpiece -- is explicitly deferred until

## 7. (mouthpiece unification), #765 (provenance/return-path), and #766

(status visibility) have landed, since this contract's entities 2-4 all
assume those are in place.

### 7.1. Acceptance checklist (this artifact's own scope)

- [x] Four entities defined with exact field shapes.
- [x] Four flows defined with exchange-ID continuity and ack separation.
- [x] Current-state assessment is honest (what landed vs. what was
      removed vs. what never existed), cross-referenced to the existing
      #767 reassessment artifact rather than re-asserting unverified
      claims.
- [x] In/out boundary stated explicitly.
- [x] Stub-fixture-vs-future-integration distinction stated explicitly.
- [ ] Guardian review (not yet requested as of this PR's creation).
