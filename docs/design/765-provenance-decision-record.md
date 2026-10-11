# #765: request provenance and return routes, decision record (second slice)

This slice turns the gap named by the merged first-slice inventory
([765-request-provenance-inventory.md](765-request-provenance-inventory.md))
into explicit decisions, states which actor owns each transition, and maps the
result onto #765's acceptance criteria. It is design only: it adds no code,
changes no runtime behavior and closes no acceptance-criteria checkbox.

This record is self-contained. Every source claim it relies on is listed in
section 2 with a file and line at base commit ecb426f, and the fixtures it
refers to are the numbered list in section 6. It does not depend on any
inventory section other than the two it cites by line in section 2.

SUPERSESSION. The merged inventory recommends two new fields,
`OriginContextID` and `OriginMessageID`, in its section 3. This record
supersedes that recommendation with the single locator in D1. The merged
inventory is not edited by this slice. The merged #766 inventory names the two
fields as a prerequisite; section 8 notes the effect on that statement.

Labels used below: DECISION is what this record proposes to settle. EVIDENCE
points to section 2 or to the #765 issue text. OPEN is a question this record
deliberately leaves for another owner.

## 1. Scope and non-goals

In scope: how one exchange records which earlier exchange it serves (the
cause link), where its answer returns (the return route), and what happens
when the link is missing, forged, duplicated or cyclic.

Out of scope, by the #765 issue text and by the current work split:

- Any code, wire change or fixture. They belong to the later slices in
  section 9.
- Pane freshness and idle observation semantics (#881) and status projections
  (#766).
- The interface-node designation, which #764 owns and has decided, and the
  cross-session authorization work that #624 and #700 own.
- A task planner, a task database, new human-approval authority, automatic
  publication or an OS sandbox (the issue's non-goals).

## 2. Evidence at base ecb426f

Each item was read at commit ecb426f, which is current main after the
latest merges. Between the earlier base 72292d3 and ecb426f, the only cited
file that changed is `internal/cli/send_message.go`, at lines 504-510 (the
verdict-gate option), outside every range cited here. A later slice must
re-verify the lines against its own base, because they drift.

- E1. The envelope carries these correlation fields and no field naming the
  message that caused a send: `internal/envelope/metadata.go:12-48`
  (`type Metadata struct`, including `ReplyTo`, `ThreadID`, `TaskID`,
  `RunID`, `InputRequestID`, `FillsInputRequestID`).
- E2. A reply slot is matched by the filler's `fills_input_request_id`
  together with its `reply_to` and sender, or, without a fill id, by
  `reply_to` and sender alone:
  `internal/projection/message_reply_slot_state.go:881-899`
  (`resolveRequestSatisfactionOpen`).
- E3. `--reply-to` is validated for syntax only (a single token that parses as
  a message filename). It is not checked against the sender's mailbox or
  session: `internal/cli/send_message.go:954-968`.
- E4. A send resolves one context for the sending session, or takes an
  explicit context flag. Nothing in that step names a context of a message
  that caused the send: `internal/cli/send_message.go:270-281`.
- E5. Bare node addresses are scoped to the source session, and cross-session
  routing is explicit only: `internal/router/router.go:24-27` and `:49-53`.
  A workspace tree alias resolves to an explicit address before routing:
  `internal/cli/send_message.go:258-264`.
- E6. A post-path message whose sender names a different session than the one
  whose `post/` holds it is dead-lettered as forged (#870):
  `internal/message/message.go:1040` (`senderClaimsForeignSession`) with the
  guard comment at `:1035-1039`. A sender that names the same session as the
  one holding the file is not checked further by this guard.
- E7. Adding an envelope field needs four places to agree: decoding at
  `internal/envelope/metadata.go:223-260` and following, the insert list at
  `:501`, the canonical key mapper at `:642-693`, and the alias list at
  `:709-739`.
- E8. The merged inventory states the gap in its section 2
  (`docs/design/765-request-provenance-inventory.md:45-64`) and recommends the
  two-field form in its section 3 (`:66-87`).
- E9. The merged #766 inventory names the two-field form as a prerequisite:
  `docs/design/766-hierarchical-status-inventory.md:92`.
- E10. A delivered record is archived at `read/<message-id>` under its
  session directory, where message-id is the complete message filename
  including its `.md` suffix. The filename is generated with that suffix
  (`internal/message/message.go:744-766`), parsing requires it (`:780-785`),
  pop archives to `read/<Filename>` (`internal/cli/pop.go:97-99` and
  `:177-180`), and the popped message ID is the envelope message ID or else
  the filename (`:460-475`).

## 3. Decisions

### 3.1. D1. One optional cause locator, named `caused_by`

DECISION. A delegating message may carry one optional envelope parameter:

```text
caused_by: <context-id>/<session-name>/<message-id>
```

It names the delivered record, in the delegating node's own session, of the
message that node was serving when it sent this one. Here message-id is the
complete message filename, including its `.md` suffix (E10), so the locator
ends in `.md`; an implementation must not append a second suffix.

- Per exchange, not per chain: siblings sent while serving one incoming
  message carry the same value and are told apart by their own message IDs.
- Optional and additive: it is absent on every existing message and on
  messages that start a chain, and both keep parsing unchanged.
- The root of a chain is found by following links until a message has none.
  No cached root and no multi-cause field are introduced.

Why a single string and not two fields. A message ID names a file inside a
session directory (E10) and does not itself name the session, so a pair of
context and message fields would still lack the session. One string names
all three parts and needs one new key at the four places in E7 instead of
two.

Alternatives considered and rejected. `ReplyTo` is checked for syntax only and
matched inside one session's reply slots (E2, E3), so reusing it for
delegation would change what it means for existing flows. `ThreadID` has no
structure or uniqueness rule that this record can rely on. Task and run
identifiers (E1) have no producer on the send path for this purpose.

### 3.2. D2. The return route is never derived from the cause link

DECISION. Each exchange answers its own source through its own reply slot
(E2). `caused_by` is correlation only. An implementation must not use it to
choose a recipient, to deliver, to fill or close a reply slot, or to skip an
intermediate node. Consequences:

- R's answer to O's delegation m2 goes to O because O sent m2, and O's
  report to T is an ordinary reply to T's m1, closing O's inbound slot for m1.
- An actionable question from O to T is a new required-reply exchange with
  its own slot and `caused_by` set to m1's delivered record. T's answer
  closes that slot only; the slot for m1 stays open.
- A cause link cannot make a result "disappear" at an intermediate node, and
  it cannot promote a parent's instruction into human approval.

### 3.3. D3. The link is correlation data, validated at send time, unverified after

DECISION. The sender CLI refuses a send whose `caused_by` does not resolve to
exactly one delivered record whose session equals the caller's session and
whose addressee is the caller. A record that exists twice with identical
content counts once; the exact test for that is OPEN for the slice that
implements it. After the send, every reader treats the value as UNVERIFIED
unless it revalidates it at consumption against the exchange that carries the
field.

DECISION. This version adds no "validated" marker field. Reason: sender
identity is checked only in part. A sender that names a foreign session is
dead-lettered (E6), but a sender that names the caller's own session is not
checked further by that guard, so same-session identity remains
client-asserted. A marker written by the same client would only be another
asserted field. A marker needs a daemon-side stamp backed by a sender check,
which is a separate decision. The no-marker decision rests on that narrower
basis. A pass of either check proves the link only relative to the asserted
same-session sender; the field is never evidence of origin, never caller
authentication and never human approval.

### 3.4. D4. Loop detection is a bounded walk, at send time and in readers

DECISION. After the validation in D3, the sender CLI walks the `caused_by`
chain upward from the cited record using only locally readable journals. It
refuses the send when the walk finds a locator it has already visited (a
cycle) or has followed more than a fixed number of links. The proposed bound
is 16 links; the exact constant is OPEN and belongs to the implementation
review.

DECISION. Every reader that walks cause links, for example the reverse lookup
in D5 or a root finder, uses the same visited set and the same bound. At the
bound or at a revisited locator it stops and reports the walk as truncated or
cyclic instead of continuing. A reader never loops and never presents a
truncated walk as complete.

- A link that cannot be resolved (for example the older context directory is
  gone, or the cited record is not found) ends the walk and does not block
  the send. A loop that passes through an unreadable context is therefore not
  detected; this is a stated residual, not a claim of completeness.
- A reader that meets an unresolvable link reports it with its reason
  (unresolved: context gone, or record not found). It never treats the last
  message it could read as the chain root, because a root is a message with
  no link, not a message whose link could not be followed.
- A message returning to a node it came from is not a loop. A question back
  to the delegating node is a valid new exchange.
- The send-time check guards against accidental forwarding loops. It does
  not defend against a hand-placed `post/` file or a client-supplied
  daemon request, which bypass the CLI.

### 3.5. D5. Duplicate relays are separate exchanges, diagnosed and never replayed

DECISION. A repeated delegation is a new message with its own message ID and
the same `caused_by`. The system does not deduplicate it, suppress it or send
it again on its own. An uncertain delivery is never grounds for a blind
resend (the issue's own rule).

DIAGNOSTIC SURFACE. Duplicates are made visible by a read-only reverse lookup:
the siblings of one cited record are the messages whose cause is that record,
and two siblings with the same recipient and content are shown as such. The
surface is a read-only reader output delivered by the later slices in
section 9 (first the projection-only slice, then the reverse lookup). It adds
no write path. Its exact form belongs to those slices; if it introduces a
status field, that field joins the single coordinated status-schema change
with #700 and #766. Until it exists, D5 is a rule about behavior, not a
shipped diagnostic.

The cost of the reverse lookup is OPEN: it reads the journals of the sessions
named by the locators, and an index is a later decision made only if a
measured need appears. Siblings found this way are UNVERIFIED client-asserted
links until the revalidation in D3 has run.

### 3.6. D6. Chains that cross a daemon restart are reported, not reconciled

DECISION. No cross-context reply-slot fill rule is introduced in this
slice. Following a cause link across a context rotation works while the older
context's session directory still exists, and is reported as unresolved
otherwise. A reply slot opened in an older context is reported as
unreconciled when its answer is sent from a newer context, until a rule
exists.

DIAGNOSTIC SURFACE. The report is the same read-only reader output as in D5.
The restart fixture F12 records what actually happens before any rule is
chosen.

OPEN. The retention rule for older context directories and the context
lifecycle decision belong to the owner of durable identity and state lifetimes;
issue #880 in the #876 epic checklist names that topic, but that epic is
outside this slice's scope, so the owner is to be assigned.

### 3.7. D7. Same-named nodes: current isolation is kept, authorization is the open part

DECISION. Same-named nodes in different sessions are isolated today by
address scoping: a bare node name always means the node in the sender's own
session, and leaving the session needs an explicit `session:node` address or a
workspace tree alias that resolves to one (E5). This record keeps that rule
and adds a regression fixture for it (F11). `caused_by` is never used to
resolve a bare name or to pick a session.

The #764 interface-node question is decided and is not this record's
concern. What remains open for the acceptance criterion on concurrent flows
is the authorization of each cross-session hop, which #624 and #700 own, and
the fixtures that prove two concurrent cross-session flows cannot cross-fill
or redirect each other. The fully qualified locator in D1 is unambiguous per
context, session and message, so D1 does not depend on that outcome.

## 4. Who owns each transition

The #765 issue separates delivery, read and claim, reply-slot fill, substantive
review and final task acceptance, and says the mouthpiece transports and
performs bounded structural checks while orchestrators and designated
reviewers own substantive decisions. This list states that ownership for the
three-level example and records what each transition does not mean.

Each entry gives the owner, then what the transition does not mean.

- Delivery to a node. Owner: the daemon, through the delivered record in the
  journal. Does not mean the node has read it.
- Read and claim. Owner: the recipient node, by popping its inbox. Does not
  mean the node accepted the request.
- Reply-slot fill. Owner: the recipient node, by a matching reply (E2). Does
  not mean task acceptance or approval.
- Intermediate acknowledge. Owner: any relaying node. Does not mean the
  original request is complete.
- Structural check at relay. Owner: the mouthpiece. Does not mean a judgement
  of correctness.
- Substantive review. Owner: the designated reviewer of that work. Does not
  mean final task acceptance.
- Final task acceptance. Owner: the accountable orchestrator of the
  originating session. Does not mean a reply-slot fill, or an upstream node's
  closure of its own slot.
- Human approval of action. Owner: the authorized human actor. A separate
  human-origin record is what proves that approval. Does not mean a parent
  agent's instruction, or a relayed message, and no relay strengthens the
  evidence.

Verdict record formats for review and acceptance belong to the owners of #406
and #639 and are not decided here. OPEN: whether the human-originated root
request needs an explicit acceptance record from the human, which is a question
for #767 and #766.

## 5. Acceptance criteria coverage

None of the following is closed by this document. It only states what the
decisions provide toward each one.

The criteria are quoted verbatim from the live #765 issue body, read through
a GET-only call on 2026-10-12 (body sha256 `949e4242fb5d...`, 49 lines). Each
entry gives the criterion text, what this record provides, and what is still
missing, naming the mechanism and its owner and not only a fixture number.
Nothing in a "Provided" entry goes beyond the decision text above.

- "A three-level request can be traced from intake through each delegation
  and back to the originating participant using durable records."
  Provided: D1 (the cause link), D2 (each answer returns by its own slot).
  Missing: the field and its sender option (slice R1), a reader that follows
  links (slices R2, R3), and fixtures F2 and F3.
- "Every hop retains its real sender, recipient, incoming reference, and
  return route; original requester information does not masquerade as caller
  authentication." Provided: D1 (the incoming reference), D2 (return route
  stays the exchange's own slot), D3 (the field is never caller
  authentication). Missing: carry-through in the sender CLI and pop output
  (slices R1, R2) and fixture F2.
- "Intermediate receipt or reply closure never silently becomes final
  acceptance; the actors responsible for review and acceptance are explicit."
  Provided: section 4, D2. Missing: verdict record formats, which the owners
  of #406 and #639 decide, and fixture F3.
- "Decision requests and replies round-trip to the exact pending item without
  weakening existing human-approval boundaries." Provided: D2 (an own slot per
  ask, so the answer fills that item only). Missing: the decision-binding
  mechanisms the issue's scope asks for, none of which D2 or D3 specify: the
  action and its scope, a revision or digest, an expiry, and the available
  human-origin evidence. Their owners are the verdict and approval producers
  and consumers tracked in #757 and #760, with record formats from the
  owners of #406 and #639. Also missing: authorization of each hop, which
  sits with #624 and #700, and fixture F4.
- "Concurrent flows and same-named mouthpieces in different sessions cannot
  cross-fill, cross-deliver, or redirect each other." Provided: D2 (no
  routing from the cause link), D7 (current address scoping kept, E5).
  Missing: cross-session hop authorization (#624, #700) and the concurrent
  flow fixtures F11 and F13.
- "Restart, timeout, late response, duplicate relay, missing route, and loop
  scenarios remain diagnosable without blind replay." Provided: D4 (loops,
  and the report of an unresolvable link), D5 (duplicates), D6 (restart).
  Missing: the read-only diagnostic surface named in D5 and D6 (slices P0,
  R2, R3), which must also report a missing route, meaning an unresolvable
  cause link or an absent return slot, with a reason; its owner is to be
  assigned. Also missing: an owner for timeout and late response diagnosis,
  which no decision here assigns; the retention rule and the cross-context
  rule for restarts (owner to be assigned, #880 names the topic); fixtures
  F7, F8 and F12.
- "Existing single-session flows remain compatible, and any new wire fields or
  migrations are documented." Provided: D1 (optional, additive). Missing: the
  four decode, insert, map and alias places kept in agreement (E7), a
  documented wire note, and fixtures F1 and F9.
- "Focused tests and repository validation pass; unrelated baseline failures
  are distinguished from this change." Provided: the fixture list in
  section 6. Missing: the implementation slices.

Conflict check against the issue text: no conflict found with D1 to D7. One
point is stated openly. The issue's scope says to "reuse existing identity and
correlation fields wherever they suffice; add only the missing relationships
after documenting the gap". D1 adds one optional field. The gap is that
`ReplyTo` is a single-session return-slot referent matched by sender (E2, E3)
and cannot express a cause across exchanges (E1, E8). The projection-only
slice in section 9 first derives per-hop linkage from the existing fields,
which keeps that rule visible.

## 6. Fixture list

These are the fixtures the later slices are expected to build. Numbers are
local to this record. Each runs through the normal producer and consumer
interfaces.

- F1. A valid send with `caused_by` is accepted and the field appears in the
  delivered envelope.
- F2. A three-level chain across three sessions (top level, owner or group,
  repository): every cause link resolves, and every return route is the
  exchange's own slot.
- F3. An intermediate acknowledge does not close the upstream slot.
- F4. An actionable question sent upward opens its own slot, and the answer
  fills that slot only.
- F5. A `caused_by` that names a record never delivered to the caller is
  refused.
- F6. A `caused_by` that names another session or another addressee is
  refused.
- F7. A cycle is refused, and a chain longer than the bound is refused; a
  reader stops at the bound and reports the walk as truncated.
- F8. A duplicate relay (same cause, same recipient) yields two exchanges
  that are both visible as siblings, and nothing is sent again.
- F9. A message without the field parses unchanged in every existing path.
- F10. The field survives both spellings and every copy-forward path, in
  agreement across the four places in E7.
- F11. Same-named nodes in two sessions: a bare address stays in the sender's
  own session, and leaving it needs an explicit address or alias (E5).
- F12. A cause link across a daemon restart: the outcome is recorded as an
  observation before any rule is chosen.
- F13. Two concurrent cross-session flows do not cross-fill, cross-deliver or
  redirect each other (needs the authorization work in #624 and #700).
- F14. A reader shows a client-asserted link as UNVERIFIED until it has been
  revalidated.

## 7. Risks and trade-offs

- A single string field is cheap to parse, but a consumer must split and
  compare three parts. All three must be present, so a partial value is
  invalid, not ambiguous.
- No validated marker (D3) keeps the trust model honest but leaves every
  consumer to revalidate. A shared helper in a later slice avoids each
  consumer inventing its own check.
- The loop bound (D4) trades completeness for a bounded cost. A chain that
  crosses an unreadable context is not checked past that point.
- Leaving D6 open and F13 dependent on #624 and #700 means the first
  implementation proves single-context chains only. That is stated, not
  hidden.

## 8. Requests for decision

These need an owner other than this slice before a later slice can claim the
matching criteria:

1. The owner and timing of cross-session hop authorization and of the
   concurrent flow fixtures, which sit with #624 and #700 (D7).
2. An owner and rule for context retention and cross-context slot
   reconciliation (D6).
3. Whether the loop bound is acceptable as proposed, and the constant (D4).
4. Whether a daemon-stamped validation marker is wanted later (D3).
5. Owners for the decision-binding mechanisms and for timeout and late
   response diagnosis (section 5).

Effect on #766. The merged #766 inventory (E9) names the two-field form as a
prerequisite for citing cross-session provenance. D1 supersedes that form with
`caused_by`. The #766 document is not edited by this slice, so a later #766
slice should cite `caused_by` instead.

## 9. Later slices and non-claims

This section orders the later slices by what each one depends on. It assigns
no owner and starts no work. A slice that touches the daemon is marked as
such, because daemon work is outside this slice and needs its own owner.

### 9.1. Order and dependencies

1. Slice P0, projection only. A read-only derivation of per-hop linkage from
   the fields that already exist (`reply_to`, `input_request_id` and
   `fills_input_request_id`, E1 and E2), with tests and a conformance table
   against this record's D2 return-route model (and E1 and E2). It adds no
   write path, mints no exchange identifier, edits no daemon code, and leaves
   the return route on the existing reply slot. It depends on nothing in the
   work of #764 or #700.
2. Slice R1, the envelope parameter and the sender CLI option (D1, D3, D4),
   proven for single-context chains. It touches the send command and the
   envelope metadata code (E4, E7). The #764 slice A change to the send
   command has merged (commit 537a963) and altered only the verdict-gate
   option, so R1 has no pending change to wait for in that file. Whether any
   part of the parsing sits on the daemon side is settled against the touch
   points when the slice starts.
3. Slice R2, the shared revalidation helper, the reader walk bound and the
   carry-through into pop output. Depends on: R1. It also checks how the typed
   pop output treats an unknown frontmatter key (listed under non-claims).
4. Slice R3, the reverse lookup that makes duplicates visible (D5). Depends
   on: R2, because showing siblings before revalidation exists would display
   forged client-asserted links as if they were checked. The index question
   stays OPEN until a measured need appears.
5. Slice R4, the restart-crossing observation (D6, F12). Depends on an owner
   for context retention (request 2). It records an outcome first and decides
   a rule afterwards. Daemon-adjacent, not scheduled here.
6. Slice R5, cross-session authorization and the concurrent flow fixtures
   (D7, F11, F13). Depends on: #624 and #700.
7. Slice R6, an optional daemon-stamped validation marker (D3). Depends on a
   decision on how the daemon establishes the sender. It is a daemon change
   and is not scheduled here.
8. Slice R7, agent-facing guidance and the note that existing flows are
   unchanged. Depends on: R1 and R2 being merged, so the text matches shipped
   behavior.

The smallest first deliverable is P0, which needs no write path and no
merged prerequisite. R1 can follow P0, since #764 slice A has merged. R3
follows R2. R4, R5 and R6 are blocked on decisions outside this record.

### 9.2. Status schema

If any slice introduces a status field, for example to surface the D5 and D6
diagnostics, that field joins one coordinated status-schema change shared
with #700 and #766, and is not bumped by a slice on its own.

### 9.3. Explicit non-claims

- This slice adds no code and changes no runtime behavior.
- It closes no #765 acceptance-criteria checkbox.
- Field name, locator shape and the loop bound are recommendations for review,
  not locked decisions.
- Not verified here: how the daemon establishes the sender it receives for a
  sender that names the caller's own session, archive retention, and whether a
  new envelope parameter appears in the typed pop output's generic frontmatter
  map.
