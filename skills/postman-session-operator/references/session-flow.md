# Session Flow Reference

Use this reference after the top-level `postman-session-operator` skill has
triggered and you need command details for live mailbox decisions.

## 1. First Commands

Use `tmux-a2a-postman get-status` for the canonical JSON status contract. Use
`tmux-a2a-postman get-status-oneline` for a compact scan across sessions. Add
`--severity` when you need contextual severity instead of compact visible-state
marks.

Use `tmux-a2a-postman inspect-input --id <message_id-or-input_request_id>` when
you need the concrete open reply-required item behind `pending` or `waiting`
without reading inbox mail.

Use `tmux-a2a-postman pop` only when you intend to claim and archive the next
inbox message. `pop` never embeds sender-authored body text inline. Read the
archived path after `pop`; prefer `markdown_absolute_path` when present.

## 2. Command Semantics

`send-heredoc` validates the auto-detected sender pane title, configured edges,
and recipient before delivery. A failed `send-heredoc` is stronger evidence
than stale footer text.

Use `--reply-required` when the recipient must answer. Use `--no-reply` for
terminal or informational mail. Without either flag, reply policy is resolved
from message metadata and ordinary message bodies are usually no-reply.

Never move runtime `post/`, `inbox/`, `read/`, or dead-letter files manually.

Footer lines such as `You can talk to:`, `Reply:`, and `No reply needed for:`
are delivery hints. When they conflict, prefer current edges, explicit body
instructions, message metadata, status output, and observed send results.

## 3. Visible State

| State         | Meaning                                       | Usual action                                         |
| ------------- | --------------------------------------------- | ---------------------------------------------------- |
| `initial`     | Pane or session has no positive live evidence | Wait for status, or verify only if workflow needs it |
| `ready`       | Pane is live with no open action or wait      | No action unless the user or workflow asks           |
| `pending`     | Inbound reply-required message is open        | `pop`, handle the message, send an exact reply       |
| `waiting`     | Outbound reply-required message is unresolved | Wait, or follow up only when timeout policy says so  |
| `stale`       | Previously known pane/session is stale        | Verify pane/session before blaming workflow          |
| `unavailable` | Status is unavailable                         | Report infrastructure unavailable to the operator    |

`pending` beats `waiting` because the node has something it can do now.
`stale` beats both because live state is not trustworthy. `initial` is neutral:
non-AI, unknown, not-yet-classified, or expected AI panes and sessions with no
response/activity should not be treated as ready until there is positive live
evidence.

## 4. Input Requests

A reply-required message opens action for the recipient and waiting state for
the sender.

`get-status` exposes concrete open input-request details at
`nodes[*].flow.input_requests.input_required` and `waiting_on_input`. Use
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

Use `--reply-required` for work requests, approval requests, status requests,
or any message where the sender needs a later resolving answer. Use
`--no-reply` for terminal or informational mail that should not create a new
wait.

When a local role template defines watchdog or timeout thresholds, treat them
as follow-up boundaries, not proof of failure. Below the boundary, prefer
`waiting`; at or beyond it, send one bounded follow-up before declaring the
recipient blocked.

For daemon-submit timeouts, a client-side timeout does not prove that the daemon
failed to claim or commit the request. When the timeout output includes a
request id, inspect that specific request with
`tmux-a2a-postman inspect-daemon-submit --id <request_id>` before retrying or
resending. Use `tmux-a2a-postman get-status --debug` for bounded aggregate
`daemon_submit` queue health such as pending, claimed, late response, and
abandoned counts.

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

`get-status` schema version 4 exposes `visible_state`, `compact`, and
contextual severity fields. Use these fields to decide whether a state is an
expected wait, live work, a blocked report, stale local evidence, or delivery
trouble.

Severity ranks from least to most urgent:

1. `ok`
2. `working`
3. `expected_wait`
4. `needs_action`
5. `blocked`
6. `attention_stale`
7. `delivery_stuck`
8. `delivery_failure`

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
text in status output.

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
   `tmux-a2a-postman pop` when ready to claim and archive the message.
4. After `pop`, use `frontmatter` for routing metadata and input-request
   identifiers, but do not decide from metadata alone. Read the complete
   archived Markdown body before any handling, routing, reply, status decision,
   or no-action or no-op decision. If your runtime only exposes bounded command
   output, read verified chunks through EOF.
5. If the popped message has `reply_policy: required`, handle it and reply with
   `--fills-input-request-id <input_request_id>` when available; keep
   `--reply-to <message_id>` for traceability. After sending, check the JSON
   `fill`, `required_input`, and `notice` fields before treating the input
   request as closed.
6. Do not send `DONE` until the completion gate passes. If evidence is missing,
   send `BLOCKED` with the failing original requirement instead.
7. If your node is `waiting` or `expected_wait`, do not clear it by reading
   mail. Wait for an exact reply or send a bounded follow-up if the workflow
   timeout requires it. For daemon-submit timeout output with a request id,
   inspect that request before deciding to retry or resend.
8. If a node is `blocked`, inspect the blocked report and resolve the named
   blocker before treating the node as stale.
9. If a node is `stale` or `attention_stale`, verify the tmux pane and session
   before resending work.
10. Audit topology and recipient names before retrying messages in dead-letter.
11. Do not edit runtime mailbox files manually.

## 9. Escalation Boundaries

Use `postman-config-auditor` when the problem looks like a missing edge, wrong
node name, stale `postman.md`, or dead-letter route.

This skill does not include infrastructure repair or low-level procedures. If
status remains unavailable or delivery stays stuck after routing checks, report
`BLOCKED` to the operator or an operator-only admin workflow.
