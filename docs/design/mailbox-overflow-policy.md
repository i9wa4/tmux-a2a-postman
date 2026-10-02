# Mailbox Overflow Policy

Tracks #840. Related: #676, #759, #839, #563, #360.

`inboxQueueCap = 20` (`internal/message/message.go:72`) is a hardcoded
per-node mailbox cap. Two different code paths currently handle delivery at
that cap, and neither gives an operator a way to see trouble coming or to
recover without manual archaeology. This document specifies the target
behavior; implementation and tests follow in a separate change against this
same issue.

## 1. Current Behavior (baseline, do not restate as the problem below)

(Line references pinned to commit `0c6578a`.)

- **Ordinary post** (`planDeliveryPolicy`,
  `internal/message/delivery_policy.go:164-175`,
  `internal/message/message.go:979-995`): at or above cap, the message is
  dead-lettered and a best-effort reactive notice is attempted
  (`sendDeadLetterNotification`, `internal/message/message.go:1220-1251`).
  The notice write can fail (logged, not retried) and second-resolution
  filename collisions are possible, so the notice is not guaranteed to
  reach or be seen by the sender. Today a
  `MailboxProjectionDeadLetteredEvent` RESOLVES the inbound and outbound
  input request and fills satisfaction
  (`internal/projection/message_reply_slot_state.go:194-208`), apart from
  an authenticated command-approval special case. This document's §2.4
  deliberately reverses that for at-cap required-reply requests; see
  §2.4.
- **Command-approval request**
  (`internal/cli/command_approval_delivery.go:71-115`,
  `internal/controlplane/controlplane.go:574-583`): a separate trusted
  direct-delivery path. At cap, an attempt already records an
  operator-visible `CommandExecutionDecided` event carrying thread id,
  command hash, and reason, and returns `status: delivery_failed`, exit
  19 (`internal/cli/execute_bash.go:366-375,425-453,1396-1412`). So the
  gap is not an absence of durable correlation; it is the absence of a
  TYPED `mailbox_full` cause distinguishing this from any other
  `delivery_failed`. Only **blocking** mode is unconditionally fail-closed
  under current mode semantics; advisory and explicit warn-only override
  can still execute despite `delivery_failed`
  (`internal/cli/execute_bash.go:1244-1275`). #840's filed wording ("must
  fail closed") is unqualified and needs reconciling with this: this
  design records "fail closed in blocking mode" as the precise claim: a
  later #840 clarification comment to the public issue text requires its
  own separate human approval, out of scope for this document.
- **Arrival ordering**: `pop` sorts claimed messages by filename
  (`internal/cli/pop.go:111-117`; also
  `internal/daemon/daemon.go:395-410` for the daemon-mediated path).
  Filenames carry only second-resolution timestamps plus a random nonce,
  so arrival order within the same second is not currently guaranteed,
  and ordinary vs. trusted-direct writers independently count then write
  without a shared linearization point
  (`internal/message/message.go:981-995`;
  `internal/controlplane/controlplane.go:574-585`).

## 2. Scope

This design covers five areas, scoped to per-node mailbox capacity only
(explicitly not #676's daemon-submit worker-pool concurrency, which #759
already addressed by moving from one global semaphore to one per session).

### 2.1. Overflow lifecycle, strict FIFO, and admission accounting

**Overflow lifecycle (fate of the NEW arrival at cap):**

- The new message is retained durably in dead-letter at cap. No eviction
  of unread mail, no implicit TTL purge, and no automatic replay.
- An explicit, audited operator replay, performed only after capacity
  frees, reuses the original message id and correlation id(s) but is
  admitted with a NEW admission sequence number, after every
  already-admitted message -- a replay never reorders ahead of existing
  mail.
- Explicit disposal (via the operator subcommand in §2.5) is the only
  terminal deletion of a dead-lettered message. Dead-lettering itself is
  not terminal by default; see §2.3/§2.4 for how this interacts with
  required-reply and command-approval state.
- If the dead-letter write itself fails, that failure must be recorded as
  a distinct, durable error condition (not silently dropped and not
  conflated with a successful dead-letter), surfaced the same way an
  ordinary delivery failure is surfaced, so an operator can detect and
  recover from a broken dead-letter path itself.
- §2.4's prior "eventually delivered" language is replaced by a
  conditional recovery outcome: a required-reply message dead-lettered at
  cap is recoverable only via the explicit audited replay above, and only
  while its request/correlation state (§2.4) has not itself been disposed
  of.

**Admission accounting (concurrency and ordering):**

- One cross-process, per-recipient admission fence serializes every
  writer (ordinary post, trusted-direct command-approval delivery) and
  every claim path (CLI `pop`, daemon-mediated pop): a blocking
  `syscall.Flock(LOCK_EX)` on an owner-only (0600) fence file under the
  session-level `mailbox-locks/<recipient>/` directory, outside every
  quarantined mailbox root, following the existing journal
  append-authority fence pattern (`internal/journal/journal.go:728-758`).
  Under the fence,
  the holder counts admitted messages, reserves the slot, assigns the
  next sequence number, persists it to a per-recipient sequence file
  (written and fsynced before the inbox file is committed), and commits
  the inbox file. Two messages arriving concurrently near the cap
  boundary must not both be admitted past the limit, and must not both
  be rejected when exactly one slot remains.
- The per-recipient high-water sequence file is the only authority for
  "never reused". It lives with the fence outside the quarantined roots,
  is initialized atomically to 0 when the recipient's admission state is
  first created, and is advanced with write-temp/fsync/rename/
  directory-fsync before any inbox commit. A missing, empty, or corrupt
  high-water file for existing admission state fails closed for operator
  repair; it is never reconstructed by scanning mailbox files. The legacy
  backfill seeds it once under the fence.
- Any failure of the lock, the count, or the commit step is fail-closed:
  the arrival is treated as not admitted (and routed to the overflow
  lifecycle above) rather than admitted without a sequence. A claim path
  that cannot take the fence fails without claiming any message.
- Legacy unsequenced inbox files are migrated by a deterministic lexical
  backfill performed under the same lock, assigning them sequence numbers
  in their existing lexical (filename) order. Mixed old/new `pop`
  implementations reading the same inbox concurrently are forbidden
  until this migration/switch completes for that inbox. Old binaries
  cannot honor the fence, so the switch is operational: every postman
  process for the session must run the new binary (restart per
  CLAUDE.md §3.1) before the backfill runs, and the new pop refuses to
  claim from an inbox whose backfill has not completed.
- FIFO is defined as the order of successful admissions under this
  linearization point, not the order messages were created or sent. An
  operator replay (above) gets a new admission position, not its
  original one.
- The generation quarantine acquires every affected recipient's fence, in
  sorted recipient order, before moving the inbox root.

### 2.2. Proactive saturation notification

- Fixed thresholds, not configurable in this issue: first warning fires
  on the crossing into 16/20 or above; it re-arms (becomes eligible to
  fire again) only after the queue drops back below 10/20. At most one
  warning fires per node per crossing (i.e., repeated arrivals while
  already at or above 16/20, without first dropping below 10/20, do not
  re-fire).
- Delivered through a cap-independent journal/status channel (for
  example an entry visible via `get-status`/an internal event journal),
  never written into any mailbox -- including, critically, never into the
  saturated mailbox itself -- so a notification can never itself trigger
  another notification or contribute to the cap it is warning about.
- This is advisory: it must not block or delay delivery of the message
  that triggered it.
- Per-node threshold configurability is an explicit non-goal of this
  design; it is left as a follow-up.

### 2.3. Command-approval-at-cap acceptance criterion

- Baseline (see §1): at cap, an attempt already fails closed in blocking
  mode, already records `CommandExecutionDecided`, and already returns
  `status: delivery_failed`, exit 19. This behavior is kept unchanged.
- What is added: a distinct capacity-failure journal event, carrying
  `thread_id`, `input_request_id`, `command_hash`, `approver`, the
  observed queue count and cap, and a timestamp; plus a structured
  `mailbox_full` reason attached under the existing `delivery_failed`
  status (not a new status value, and not exit code other than 19).
- In blocking mode, a retry reuses #823's existing pending-thread
  redeliver path: no new approval request is minted, and the underlying
  command is never executed twice.
- Advisory mode and explicit warn-only-with-override retain their
  current documented behavior (they may still execute despite
  `delivery_failed`); this design does not change mode semantics, only
  blocking mode's failure typing.
- This path is tested separately from the ordinary dead-letter path (see
  §4); it currently has no dedicated test coverage for cap behavior.
- #840's filed issue text says command-approval "must fail closed"
  without qualification; this design records the precise claim as "fail
  closed in blocking mode." Correcting the public issue text itself is a
  separate action requiring its own human approval and is out of scope
  here.

### 2.4. `reply_policy: required` acceptance criterion

Baseline correction (see §1): today, a `MailboxProjectionDeadLetteredEvent`
resolves both the inbound and outbound input request and fills
satisfaction (apart from the authenticated command-approval special
case) -- i.e. dead-lettering is currently TERMINAL for required-reply
state. This design deliberately reverses that for the at-cap case:

- **At-cap REQUEST** (an inbound `reply_policy: required` message that
  cannot be admitted because the target is at cap): dead-lettering this
  arrival is non-terminal. The exact original `input_request_id` is
  retained. The sender/coordinator receives a durable failure signal (at
  dead-letter time) and, separately, a durable recovery signal once an
  explicit operator replay (§2.1) succeeds.
- **At-cap REPLY** (a reply to an existing required-input request, which
  cannot be admitted because the recipient's inbox is at cap): the
  original `fills_input_request_id` is retained across the dead-letter.
  The fill is not considered closed by the dead-letter event itself.
- Exactly one event closes the fill: the successful re-admission of the
  replayed reply (not the original dead-letter event, and not a second,
  duplicate fill created independently of the replay).
- Authenticated command-approval reply semantics are unchanged by this
  section; the existing special case in
  `internal/projection/message_reply_slot_state.go` continues to apply to
  command-approval replies as today.
- Tested separately from the ordinary path (see §4): no lost and no
  duplicate fill across a retry or a daemon restart.
- Failure and recovery signals are journal events delivered through the
  same cap-independent channel as §2.2 (visible via
  `get-status`/inspect, carrying `input_request_id` or
  `fills_input_request_id`, message id, recipient, count/cap, and time).
  They are never written into any mailbox, so they cannot themselves
  overflow.
- Operator disposal (§2.5) of an at-cap required REQUEST closes its
  input request with a distinct terminal `disposed` outcome and emits
  that outcome to the sender through the channel above. Disposal of an
  at-cap REPLY leaves the original input request OPEN (the requester may
  still receive a different reply) and records the disposal against its
  `fills_input_request_id`. Neither case creates a fill.

### 2.5. Backlog recovery/drain tooling

- Listing and triage are READ-ONLY: visibility into what is dead-lettered
  or pending is exposed via `get-status`/an inspect-style command. This
  alone can never mutate mailbox state.
- A separate, explicitly mutating operator subcommand may replay or
  dispose exactly ONE identified message at a time (never a bulk
  operation). It requires the caller's tmux pane title to equal the
  configured `ui_node` (the same pane-title authentication
  `--record-decision` uses; a coordination control, not an OS security
  boundary) and an explicit reason, defaults to a dry-run (no mutation)
  unless a flag confirms the real run, and atomically records an audit
  entry (actor, message id, old state/position, new state/position, and
  the effect on any input-request/approval correlation it touches).
- "Reprioritize" is dropped from scope entirely: it conflicts with
  strict FIFO (§2.1) and is not needed to satisfy #840.
- Command-approval request messages are never replayable or disposable
  through this tooling by any caller; their lifecycle stays with #823
  (TTL expiry, supersede, pending-thread redeliver). An attempt is
  refused, and the refusal is audited the same way a successful disposal
  is.

## 3. Non-Goals

- Changing #676's daemon-submit worker-pool concurrency behavior (already
  addressed by #759).
- Raising, lowering, or making `inboxQueueCap` configurable as a standalone
  change; if a future change makes it configurable, that is tracked
  separately.
- Any eviction-on-overflow or bulk-reprioritization policy beyond the
  single-message, audited replay/dispose path in §2.1/§2.5.
- Per-node configurability of the §2.2 saturation thresholds (left as a
  follow-up).
- Changing advisory/warn-only-override mode semantics for command
  approvals (§2.3): only blocking mode's failure typing changes.

## 4. Verification Plan

Issue #563's existing soak validator
(`docs/design/daemon-soak-validation.md`,
`scripts/validation/daemon_soak_check.go:58-61,123-151`) checks
daemon-submit and memory thresholds; its final-late-response-count-and-age
pass criteria establish daemon-submit health, not mailbox correctness, and
remain in place as an independent check unrelated to this design.

Mailbox-specific verification, on disposable sessions with a controlled
clock and injected faults (never against this session's own live
mailbox, and never manipulating real production mailbox state):

- Exactly one admission results when two writers race for the same last
  slot at cap, and the inbox never exceeds cap (§2.1).
- FIFO (admission order) holds across same-second arrivals, mixed
  ordinary/trusted-direct writers, a daemon restart mid-sequence, and the
  legacy unsequenced-file migration/backfill (§2.1).
- A retained overflow message replays exactly once under an explicit
  operator replay, landing at a new admission position (§2.1).
- An at-cap required request stays open, then produces a failure signal,
  then (after replay) a recovery signal, then exactly one fill-closing
  event -- never a lost fill, never a duplicate fill (§2.4).
- A blocking-mode command-approval attempt at cap returns
  `delivery_failed` with the `mailbox_full` reason and a run count of
  zero (the command never executes) (§2.3).
- A saturation warning fires at most once per crossing into 16/20, and
  resets (re-arms) only after the queue drops below 10/20 and the node
  drains and re-saturates (§2.2).
- An unauthorized attempt to drain or dispose of an approver's pending
  request is refused, and the refusal is itself audited (§2.5).
- An injected dead-letter write failure is recorded as its distinct
  durable error, never as a successful dead-letter (§2.1).
- An injected fence, count, or commit failure leaves the arrival not
  admitted, and a claim that cannot take the fence claims nothing
  (§2.1).
- Disposal of an at-cap required request yields exactly one `disposed`
  outcome; disposal of an at-cap reply leaves the request open with no
  fill (§2.4).
- A lost, empty, or corrupt high-water file fails closed and never
  yields a reused sequence; a crash at any persist phase leaves either
  the prior or the new value (§2.1).

## 5. Resolved Open Choices

(Superseding the prior "Open Questions" section; each choice below was
reviewed and adopted. A future change may revisit any of these with a
reasoned alternative and a fresh design review.)

- **Saturation threshold**: fixed at first-crossing 16/20, re-arm below
  10/20, at most one warning per node per crossing; not configurable in
  this issue.
- **Command-approval-at-cap record shape**: a distinct capacity-failure
  journal event (not the ordinary dead-letter journal shape), carrying a
  structured `mailbox_full` reason, under the existing `delivery_failed`
  status with exit code 19 unchanged.
- **Drain/recovery interface**: read-only visibility through
  `get-status`/an inspect command; mutation (replay or dispose) only
  through a separate, explicit, audited operator subcommand acting on one
  identified message at a time, defaulting to dry-run.
