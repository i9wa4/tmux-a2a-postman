---
name: postman-session-operator
license: MIT
description: |
  Operate and diagnose live tmux-a2a-postman sessions.
  Use when:
  - Interpreting get-health or get-health-oneline output
  - Deciding whether to pop, reply, resend, wait, or restart
  - Diagnosing pending, waiting, stale, unread, post queue, or dead-letter state
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

## 2. Visible State

| State         | Meaning                                       | Usual action                                        |
| ------------- | --------------------------------------------- | --------------------------------------------------- |
| `ready`       | Pane is live with no open action or wait      | No action unless the user or workflow asks          |
| `pending`     | Inbound reply-required message is open        | `pop`, handle the message, reply with `--reply-to`  |
| `waiting`     | Outbound reply-required message is unresolved | Wait, or follow up only when timeout policy says so |
| `stale`       | Pane or session is missing or unknown         | Verify pane/session before blaming workflow         |
| `unavailable` | Daemon cannot provide canonical health        | Check daemon and session ownership                  |

`pending` beats `waiting` because the node has something it can do now.
`stale` beats both because live state is not trustworthy.

## 3. Reply Obligations

A reply-required message opens action for the recipient and waiting state for
the sender.

A resolving reply must name the original message id:

```sh
tmux-a2a-postman send --to <sender> --body "<reply>" --reply-to <message-id>
```

Reading with `pop` clears unread state, but it does not clear reply-required
action. Only a later message with exact `--reply-to <message-id>` clears the
obligation.

Use `--no-reply` for terminal or informational mail that should not create a
new wait.

## 4. Queue Signals

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

## 5. Safe Operator Flow

1. Run `tmux-a2a-postman get-health`.
2. If your node is `pending`, run `tmux-a2a-postman pop`.
3. If the popped message has `reply_policy: required`, handle it and reply with
   `--reply-to <message-id>`.
4. If your node is `waiting`, do not clear it by reading mail. Wait for an
   exact reply or send a bounded follow-up if the workflow timeout requires it.
5. If a node is `stale`, verify the tmux pane, tmux session, and daemon before
   resending work.
6. If messages are in dead-letter, audit topology and recipient names before
   retrying.
7. Do not edit `post/`, `inbox/`, `read/`, or dead-letter files manually.

## 6. Escalation Boundaries

Use `postman-config-auditor` when the problem looks like a missing edge, wrong
node name, stale `postman.md`, or dead-letter route.

Use normal daemon operations only after health suggests the daemon cannot
observe the session or delivery is stuck despite valid topology.
