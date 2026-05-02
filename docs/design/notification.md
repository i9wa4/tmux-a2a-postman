# Notification Design

`tmux-a2a-postman` now keeps notification behavior intentionally small. The
daemon delivers mail to the recipient inbox, sends a pane hint to that recipient
when delivery succeeds, and emits startup auto-PING messages. It does not run a
separate policy layer for operator escalation.

## 1. Surfaces

| Surface       | Trigger                         | Destination        |
| ------------- | ------------------------------- | ------------------ |
| Inbox file    | Routed `send` or daemon PING    | `inbox/{node}/`    |
| Pane hint     | Successful inbox delivery       | Recipient tmux pane |
| Health JSON   | `get-health`                    | stdout             |
| Health line   | `get-health-oneline`            | stdout             |
| Default TUI   | Daemon runtime health snapshots | daemon pane        |

## 2. Delivery Path

1. `send` writes a message request.
2. The daemon validates the route from `edges`.
3. Valid mail is moved into `inbox/{node}/`.
4. The recipient pane receives `notification_template`.
5. The recipient reads and archives the message with `pop`.

Unroutable mail goes to `dead-letter/`. Dead-letter handling embeds its own
manual recovery guidance and is separate from normal pane hints.

## 3. Health Model

get-health, get-health-oneline, and the default TUI are three views over the
same canonical contract.

| State     | Meaning                                  | Compact mark |
| --------- | ---------------------------------------- | ------------ |
| `ready`   | Pane is live and has no unread inbox mail | green mark   |
| `pending` | Node has unread inbox mail               | blue diamond |
| `stale`   | Pane is stale, missing, or unavailable   | red mark     |

The health payload exposes `queues.post_count`, `queues.inbox_count`, and
`queues.dead_letter_count` for mailbox backlog checks. Per-node state is
reported as `nodes[*].visible_state`.

## 4. Configuration

The remaining notification-related public settings are:

| Field                     | Purpose                                           |
| ------------------------- | ------------------------------------------------- |
| `notification_template`   | Pane hint rendered when mail arrives              |
| `message_footer`          | Reply guidance appended to stored `send` mail     |
| `daemon_message_template` | Envelope body for daemon-originated startup PING  |
| `ui_node`                 | Optional target filter for startup auto-PING      |

`ui_node` is not a general escalation channel. When empty, startup auto-PING may
target all discovered nodes. When set, startup auto-PING is limited to that node
if it is discovered in the active session.
