---
name: postman-session-operator
license: MIT
description: |
  Operate and diagnose live tmux-a2a-postman sessions.
  Use when:
  - Interpreting get-health or get-health-oneline output
  - Deciding whether to pop, reply, resend, wait, follow up, or restart
  - Handling reply-required, no-reply, reply-to, exact reply-slot replies, or
    status request behavior
  - Diagnosing pending, waiting, blocked, stale, unread, post queue,
    dead-letter, auto-ping, pane discovery, daemon restart, or slow delivery
    state
  Do not use for topology or postman.md syntax audits; use postman-config-auditor.
  Do not use only to send the first message; use postman-send-message.
---

# postman-session-operator

Use this skill to read live tmux-a2a-postman session state and choose the next
safe operator action.

## 1. First Commands

Use `tmux-a2a-postman get-health` when making a session decision. It returns
the canonical JSON health contract.

Use `tmux-a2a-postman get-health-oneline` for a compact scan across sessions.
Add `--severity` when you need the opt-in contextual severity token instead of
the legacy compact visible-state marks.

Use `tmux-a2a-postman inspect-reply --id <message_id-or-reply_slot_id>` when
you need the concrete open reply-required item behind `pending` or `waiting`
without reading inbox mail.

Use `tmux-a2a-postman pop` only when you intend to read and archive the next
inbox message.

## 2. Command Semantics

`send-heredoc` validates the auto-detected sender pane title, configured edges,
and the recipient before delivery. A failed `send-heredoc` is stronger
evidence than stale footer text.

Use `--reply-required` when the recipient must answer. Use `--no-reply` to force
an informational message. Without either flag, the reply policy is resolved from
message metadata and ordinary message bodies are usually no-reply.

`pop` reads and archives the next unread inbox message in one step. Do not run
it for diagnostics where archiving would be wrong. Never move runtime `post/`,
`inbox/`, `read/`, or dead-letter files manually.

Footer lines such as `You can talk to:`, `Reply:`, and `No reply needed for:`
are delivery hints. When they conflict, prefer current edges, explicit body
instructions, message metadata, health output, and observed send results.

## 3. Visible State

| State         | Meaning                                       | Usual action                                        |
| ------------- | --------------------------------------------- | --------------------------------------------------- |
| `ready`       | Pane is live with no open action or wait      | No action unless the user or workflow asks          |
| `pending`     | Inbound reply-required message is open        | `pop`, handle the message, send an exact reply      |
| `waiting`     | Outbound reply-required message is unresolved | Wait, or follow up only when timeout policy says so |
| `stale`       | Pane or session is missing or unknown         | Verify pane/session before blaming workflow         |
| `unavailable` | Daemon cannot provide canonical health        | Check daemon and session ownership                  |

`pending` beats `waiting` because the node has something it can do now.
`stale` beats both because live state is not trustworthy.

## 4. Reply Slots

A reply-required message opens action for the recipient and waiting state for
the sender.

`get-health` exposes concrete open reply-slot details at
`nodes[*].flow.reply_slots.action_required` and `waiting_on_reply`. Each detail
includes `direction`, `message_id`, `reply_slot_id`, `sender`, `recipient`,
`reply_policy`, and available open/read timestamps. Use `inspect-reply --id`
for a focused lookup by `message_id` or `reply_slot_id`.

A new reply-required message carries an exact `reply_slot_id`. A resolving
reply should fill that slot:

```sh
tmux-a2a-postman send-heredoc --to <sender> --fills-reply-slot-id <reply-slot-id> --reply-to <message-id> <<'POSTMAN_BODY'
<reply>
POSTMAN_BODY
```

Use quoted heredoc stdin for non-interactive replies. The single quotes around
`POSTMAN_BODY` preserve literal command substitutions, backticks, `$HOME`
variables, quotes, code fences, and shell examples. Do not pass reply bodies
through argv, file-body shortcuts, or generic pipe-oriented guidance.

Reading with `pop` clears unread state, but it does not clear reply-required
action. Only a later message with `--fills-reply-slot-id <reply-slot-id>`
clears an exact reply slot. `--reply-to <message-id>` remains useful for
fallback message-link closure and human traceability.

Use `--reply-required` for work requests, approval requests, status requests,
or any message where the sender needs a later resolving answer. Use
`--no-reply` for terminal or informational mail that should not create a new
wait.

When a local role template defines watchdog or timeout thresholds, treat them
as follow-up boundaries, not proof of failure. Below the boundary, prefer
`waiting`; at or beyond it, send one bounded follow-up before declaring the
recipient blocked.

## 5. Queue Signals

| Field                      | Meaning                                  |
| -------------------------- | ---------------------------------------- |
| `queues.post_count`        | Messages still waiting in the post queue |
| `queues.inbox_count`       | Unread inbox messages                    |
| `queues.dead_letter_count` | Delivery failed or route unavailable     |
| `action_required_count`    | Inbound required replies still open      |
| `waiting_on_reply_count`   | Outbound required replies still open     |
| `info_unread_count`        | Unread no-reply messages                 |

If dead letters exist, treat routing or configuration as suspect and use
`postman-config-auditor` before manually retrying delivery.

## 6. Contextual Severity

`get-health` schema version 3 keeps the legacy `visible_state` and `compact`
fields stable and adds contextual severity fields. Use these fields to decide
whether a state is an expected wait, live work, a blocked report, stale local
evidence, or delivery trouble.

Severity ranks from least to most urgent:

1. `ok`
2. `working`
3. `expected_wait`
4. `needs_action`
5. `blocked`
6. `attention_stale`
7. `delivery_stuck`
8. `delivery_failure`

| Field                 | Use                                              |
| --------------------- | ------------------------------------------------ |
| `severity`            | Worst contextual severity for the session/node   |
| `severity_source`     | Surface that produced that severity              |
| `severity_reason`     | Short reason for the chosen severity             |
| `compact_severity`    | ASCII token used by `get-health-oneline --severity` |
| `delivery`            | Post queue and dead-letter delivery health       |
| `nodes[*].node_local` | Pane-local activity/staleness evidence           |
| `nodes[*].flow`       | Reply-slot and blocked-report workflow evidence  |
| `nodes[*].queues`     | Node queue counts                                |

Interpretation rules:

1. `expected_wait` means a reply-required response is still expected. Wait or
   follow the workflow timeout policy; do not treat this as blocked by itself.
2. `needs_action` means the local node owes a reply. Pop and answer with the
   exact reply slot when available.
3. `blocked` means an open blocked report exists. Structured
   `blocked_report` metadata is proven evidence; an exact first-line `BLOCKED:`
   report is inferred evidence and appears with `?` in compact severity.
4. `delivery_stuck` means the oldest pending post item is at least 180 seconds
   old. Inspect delivery before sending more traffic.
5. `delivery_failure` means dead-letter files exist. Audit routing before
   retrying.

## 7. Screen Progress

`nodes[*].screen_progress` is non-content pane evidence. Use it to distinguish
a pane that is changing from one that is merely quiet; do not expect raw pane
text in health output.

| Field                                   | Meaning                                              |
| --------------------------------------- | ---------------------------------------------------- |
| `screen_progress.evidence_state`        | `missing`, `stale`, `changed`, or `unchanged`        |
| `screen_progress.last_capture_at`       | Last pane capture timestamp when available           |
| `screen_progress.last_screen_change_at` | Last detected screen-change timestamp when available |
| `screen_progress.screen_fingerprint`    | Opaque screen fingerprint, not transcript content    |

`get-health-oneline` omits this detail to stay compact; use `get-health` when
progress evidence matters.

## 8. Safe Operator Flow

1. Run `tmux-a2a-postman get-health`.
2. If `severity` is `delivery_failure` or `delivery_stuck`, inspect delivery
   and topology before creating more messages.
3. If your node is `pending` or `needs_action`, inspect
   `nodes[*].flow.reply_slots.action_required` or run
   `tmux-a2a-postman inspect-reply --id <message_id-or-reply_slot_id>` when you
   need the exact open item before reading. Then run `tmux-a2a-postman pop`
   when you are ready to handle and archive the message.
4. If the popped message has `reply_policy: required`, handle it and reply with
   `--fills-reply-slot-id <reply_slot_id>` when the pop output includes
   `reply_slot_id`; keep `--reply-to <message_id>` for traceability when the
   footer provides it. Otherwise use `--reply-to <message_id>` as fallback
   closure.
5. If your node is `waiting` or `expected_wait`, do not clear it by reading
   mail. Wait for an exact reply or send a bounded follow-up if the workflow
   timeout requires it.
6. If a node is `blocked`, inspect the blocked report and resolve the named
   blocker before treating the node as stale.
7. If a node is `stale` or `attention_stale`, verify the tmux pane, tmux
   session, and daemon before resending work.
8. If messages are in dead-letter, audit topology and recipient names before
   retrying.
9. Do not edit `post/`, `inbox/`, `read/`, or dead-letter files manually.

## 9. Escalation Boundaries

Use `postman-config-auditor` when the problem looks like a missing edge, wrong
node name, stale `postman.md`, or dead-letter route.

Use normal daemon operations only after health suggests the daemon cannot
observe the session or delivery is stuck despite valid topology.
