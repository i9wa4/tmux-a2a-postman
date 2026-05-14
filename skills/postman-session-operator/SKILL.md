---
name: postman-session-operator
license: MIT
description: |
  Operate live tmux-a2a-postman message workflows.
  Use when:
  - Interpreting get-status or get-status-oneline output
  - Deciding whether to pop, reply, resend, wait, or follow up
  - Handling reply-required, no-reply, reply-to, exact input-request replies, or
    status request behavior
  - Diagnosing pending, waiting, blocked, stale, unread, post queue,
    dead-letter, pane discovery, or slow delivery state
  Do not use for topology or postman.md syntax audits; use postman-config-auditor.
  Do not use only to send the first message; use postman-send-message.
  Do not use for infrastructure management or low-level checks.
---

# postman-session-operator

Use this skill to read live tmux-a2a-postman message state and choose the next
safe agent action. This skill does not authorize inspecting or managing postman
infrastructure.

## 1. First Commands

Use `tmux-a2a-postman get-status` when making a session decision. It returns
the canonical JSON health contract.

Use `tmux-a2a-postman get-status-oneline` for a compact scan across sessions.
Add `--severity` when you need the opt-in contextual severity token instead of
the default compact visible-state marks.

Use `tmux-a2a-postman inspect-input --id <message_id-or-input_request_id>` when
you need the concrete open reply-required item behind `pending` or `waiting`
without reading inbox mail.

Use `tmux-a2a-postman inspect-message --id <message_id>` as a read-only
historical lookup when you need a persisted message after it was read,
archived, or no longer tied to an open input request. Use `--path` for the
stored Markdown path and `--body` for sender-authored body text.

Use `tmux-a2a-postman pop` only when you intend to claim and archive the next
inbox message.

## 2. Command Semantics

`send-heredoc` validates the auto-detected sender pane title, configured edges,
and the recipient before delivery. A failed `send-heredoc` is stronger
evidence than stale footer text.

Use `--reply-required` when the recipient must answer. Use `--no-reply` to force
an informational message. Without either flag, the reply policy is resolved from
message metadata and ordinary message bodies are usually no-reply.

`pop` claims and archives the next unread inbox message in one step. Do not run
it for diagnostics where archiving would be wrong. Never move runtime `post/`,
`inbox/`, `read/`, or dead-letter files manually. The JSON output identifies
the archived Markdown with `markdown_path` and exposes structured
`frontmatter`; `markdown_path` may be display-shortened with `~`, so use
`markdown_absolute_path` when present for programmatic reads. `pop` never
embeds sender-authored body text inline; when sender-authored content is
needed, read the archived path after `pop` instead of expecting inline
body/content in the JSON.

After every successful `pop` with `status=message`, read the complete archived
Markdown body before any handling, routing, reply, status decision, or
no-action or no-op decision.
`messageType: ping`, `replyPolicy: none`, and other metadata do not allow
skipping the body. Truncated command output from `cat`, `sed`, `rg`, shell
logs, or other bounded stdout paths is not a valid archived-body read. If a
runtime only exposes bounded stdout, read verified chunks through EOF or stop
with a clear body-not-fully-read state.

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
| `unavailable` | Status is unavailable                         | Report infrastructure unavailable to the operator   |

`pending` beats `waiting` because the node has something it can do now.
`stale` beats both because live state is not trustworthy.

## 4. Input Requests

A reply-required message opens action for the recipient and waiting state for
the sender.

`get-status` exposes concrete open input-request details at
`nodes[*].flow.input_requests.input_required` and `waiting_on_input`. Each
detail includes `direction`, `message_id`, `input_request_id`, `sender`,
`recipient`, `reply_policy`, available open/read timestamps, and durable
journal event IDs when known. Use
`inspect-input --id` for a focused lookup by `message_id` or
`input_request_id`.

A new reply-required message carries an exact `input_request_id`. A resolving
reply should fill that slot:

```sh
tmux-a2a-postman send-heredoc --to <sender> --fills-input-request-id <input-request-id> --reply-to <message-id> <<'POSTMAN_BODY'
<reply>
POSTMAN_BODY
```

Use quoted heredoc stdin for non-interactive replies. The single quotes around
`POSTMAN_BODY` preserve literal command substitutions, backticks, `$HOME`
variables, quotes, code fences, and shell examples. Do not pass reply bodies
through argv, file-body shortcuts, or generic pipe-oriented guidance.

Reading with `pop` clears unread state, but it does not clear reply-required
action. Only a later message with `--fills-input-request-id <input-request-id>`
clears an exact input request. `--reply-to <message-id>` remains useful for
fallback message-link closure and human traceability.

Filling an input request closes transport, not task acceptance. Before `DONE`,
compare the original requirements/checklist against actual evidence. Use this
compact proof shape when work is complete:

```text
DONE: Requirements satisfied.
Task artifact: <artifact-reference>
Original checklist: PASS
Evidence: <commands, issue/PR links, tests, or verification output>
Remaining blockers: none
```

Use `BLOCKED` with `Original checklist: FAIL` when any requested item is
unresolved or unverified. Receivers should verify checklist status, durable
references, evidence, and blockers before relaying, approving, or closing work.

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
| `input_required_count`     | Inbound required replies still open      |
| `waiting_on_input_count`   | Outbound required replies still open     |
| `info_unread_count`        | Unread no-reply messages                 |

If dead letters exist, treat routing or configuration as suspect and use
`postman-config-auditor` before manually retrying delivery.

## 6. Contextual Severity

`get-status` schema version 3 exposes `visible_state`, `compact`, and
contextual severity fields. Use these fields to decide
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

| Field                 | Use                                                 |
| --------------------- | --------------------------------------------------- |
| `severity`            | Worst contextual severity for the session/node      |
| `severity_source`     | Surface that produced that severity                 |
| `severity_reason`     | Short reason for the chosen severity                |
| `compact_severity`    | ASCII token used by `get-status-oneline --severity` |
| `delivery`            | Post queue and dead-letter delivery health          |
| `nodes[*].node_local` | Pane-local activity/staleness evidence              |
| `nodes[*].flow`       | Input-request and blocked-report workflow evidence  |
| `nodes[*].queues`     | Node queue counts                                   |

Interpretation rules:

1. `expected_wait` means a reply-required response is still expected. Wait or
   follow the workflow timeout policy; do not treat this as blocked by itself.
2. `needs_action` means the local node owes a reply. Pop and answer with the
   exact input request when available.
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

`get-status-oneline` omits this detail to stay compact; use `get-status` when
progress evidence matters.

## 8. Safe Operator Flow

1. Run `tmux-a2a-postman get-status`.
2. If `severity` is `delivery_failure` or `delivery_stuck`, inspect delivery
   and topology before creating more messages.
3. If your node is `pending` or `needs_action`, inspect
   `nodes[*].flow.input_requests.input_required` or run
   `tmux-a2a-postman inspect-input --id <message_id-or-input_request_id>` when
   you need the exact open item before reading. Then run
   `tmux-a2a-postman pop` when you are ready to claim and archive the message.
4. After `pop`, use `frontmatter` for routing metadata and input-request
   identifiers, but do not make handling, routing, reply, status, no-action, or
   no-op decisions from metadata alone. Read the complete archived Markdown body
   by opening the returned
   `markdown_absolute_path` when present, otherwise `markdown_path`. If your
   runtime only exposes bounded command output, read verified chunks through
   EOF before any handling, routing, reply, status decision, or no-action or
   no-op decision.
   `messageType: ping`, `replyPolicy: none`, and other metadata do not waive
   this complete-body read. Default pop JSON does not include inline
   body/content, and truncated command output does not count as a complete read.
5. If the popped message has `reply_policy: required`, handle it and reply with
   `--fills-input-request-id <input_request_id>` when the pop output includes
   `input_request_id`; keep `--reply-to <message_id>` for traceability when the
   footer provides it. Otherwise use `--reply-to <message_id>` as fallback
   closure.
6. Do not send `DONE` until the completion gate passes. If evidence is missing,
   send `BLOCKED` with the failing original requirement instead.
7. If your node is `waiting` or `expected_wait`, do not clear it by reading
   mail. Wait for an exact reply or send a bounded follow-up if the workflow
   timeout requires it.
8. If a node is `blocked`, inspect the blocked report and resolve the named
   blocker before treating the node as stale.
9. If a node is `stale` or `attention_stale`, verify the tmux pane and session
   before resending work. If status remains unavailable, report the
   infrastructure problem instead of attempting repair or low-level checks.
10. Audit topology and recipient names before retrying messages in dead-letter.
11. Do not edit `post/`, `inbox/`, `read/`, or dead-letter files manually.

## 9. Escalation Boundaries

Use `postman-config-auditor` when the problem looks like a missing edge, wrong
node name, stale `postman.md`, or dead-letter route.

This skill does not include infrastructure repair or low-level procedures. If
status remains unavailable or delivery stays stuck after routing checks, report
`BLOCKED` to the operator or an operator-only admin workflow.
