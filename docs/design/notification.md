# Notification Design

`tmux-a2a-postman` now keeps notification behavior intentionally small. The
daemon delivers mail to the recipient inbox, sends a pane hint to that recipient
when delivery succeeds, and emits auto-PING messages when the daemon starts or
when a node appears. If the same role reappears with a new pane ID, that
replacement pane is treated as newly appeared. It does not run a separate policy
layer for operator escalation.

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

| State     | Meaning                                             | Compact mark |
| --------- | --------------------------------------------------- | ------------ |
| `ready`   | Pane is live with no open action or wait            | green mark   |
| `waiting` | Node is waiting for a reply-required response       | yellow mark  |
| `pending` | Node has inbound reply-required action              | blue diamond |
| `stale`   | Pane or session is missing, unavailable, or unknown | red mark     |

A live pane that simply has not changed for a long time is internally `idle`
and remains `ready` in the visible health model.

Session fallback may report `unavailable` when this daemon cannot provide
canonical health for a tmux session. It is displayed as red, but it is not a
per-node state.

The health payload exposes `queues.post_count`, `queues.inbox_count`,
`queues.dead_letter_count`, and per-node reply-obligation counts for mailbox
backlog checks. Per-node state is reported as `nodes[*].visible_state`.

## 4. Configuration

The remaining notification-related public settings are:

| Field                      | Purpose                                          |
| -------------------------- | ------------------------------------------------ |
| `notification_template`    | Pane hint rendered when mail arrives             |
| `message_footer`           | Reply guidance appended to stored `send` mail    |
| `daemon_message_template`  | Envelope body for daemon-originated startup PING |
| `ui_node`                  | Optional target filter for startup auto-PING     |
| `auto_enable_new_sessions` | Auto-enable sessions with configured node panes  |

`ui_node` is not a general escalation channel. When empty, startup auto-PING may
target all discovered nodes. When set, startup auto-PING is limited to that node
if it is discovered in an enabled session. `auto_enable_new_sessions` defaults
to true, so a single user daemon can discover project sessions that already have
configured node panes.
