---
name: postman-session-operator
license: MIT
description: |
  Operate and diagnose live tmux-a2a-postman sessions.
  Use when:
  - Interpreting get-health or get-health-oneline output
  - Deciding whether to pop, reply, resend, wait, follow up, or restart
  - Handling reply-required, no-reply, reply-to, exact obligation replies, or
    status request behavior
  - Diagnosing pending, waiting, stale, unread, post queue, dead-letter,
    auto-ping, pane discovery, daemon restart, or slow delivery state
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

Use `tmux-a2a-postman pop` only when you intend to read and archive the next
inbox message.

## 2. Command Semantics

`send` validates the auto-detected sender pane title, configured edges, and the
recipient before delivery. A failed send is stronger evidence than stale footer
text.

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

## 4. Reply Obligations

A reply-required message opens action for the recipient and waiting state for
the sender.

A new reply-required message carries an exact `obligation_id`. A resolving
reply should name that obligation:

```sh
tmux-a2a-postman send --to <sender> --body '<reply>' --satisfies-obligation-id <obligation-id> --reply-to <message-id>
```

For shell-sensitive or multiline replies, use `--body-file <path>` or
`--body-stdin` with the same reply flags. This preserves literal command
substitutions, backticks, `$HOME` variables, quotes, and shell examples.

Reading with `pop` clears unread state, but it does not clear reply-required
action. Only a later message with `--satisfies-obligation-id <obligation-id>`
clears an exact obligation. `--reply-to <message-id>` remains useful for
legacy messages and human traceability.

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

## 6. Screen Progress

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

## 7. Safe Operator Flow

1. Run `tmux-a2a-postman get-health`.
2. If your node is `pending`, run `tmux-a2a-postman pop`.
3. If the popped message has `reply_policy: required`, handle it and reply with
   `--satisfies-obligation-id <obligation_id>` when the pop output includes
   `obligation_id`; keep `--reply-to <message_id>` for traceability when the
   footer provides it. Otherwise use legacy `--reply-to <message_id>`.
4. If your node is `waiting`, do not clear it by reading mail. Wait for an
   exact reply or send a bounded follow-up if the workflow timeout requires it.
5. If a node is `stale`, verify the tmux pane, tmux session, and daemon before
   resending work.
6. If messages are in dead-letter, audit topology and recipient names before
   retrying.
7. Do not edit `post/`, `inbox/`, `read/`, or dead-letter files manually.

## 8. Escalation Boundaries

Use `postman-config-auditor` when the problem looks like a missing edge, wrong
node name, stale `postman.md`, or dead-letter route.

Use normal daemon operations only after health suggests the daemon cannot
observe the session or delivery is stuck despite valid topology.
