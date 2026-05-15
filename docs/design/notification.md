# Notification Design

`tmux-a2a-postman` now keeps notification behavior intentionally small. The
daemon delivers mail to the recipient inbox, sends a pane hint to that recipient
when delivery succeeds, and emits auto-PING messages when the daemon starts or
when a node appears. If the same role reappears with a new pane ID, that
replacement pane is treated as newly appeared. It does not run a separate policy
layer for operator escalation.

## 1. Surfaces

| Surface     | Trigger                              | Destination         |
| ----------- | ------------------------------------ | ------------------- |
| Inbox file  | Routed `send-heredoc` or daemon PING | `inbox/{node}/`     |
| Pane hint   | Successful inbox delivery            | Recipient tmux pane |
| Status JSON | `get-status`                         | stdout              |
| Status line | `get-status-oneline`                 | stdout              |
| Default TUI | Daemon runtime status snapshots      | daemon pane         |

## 2. Delivery Path

1. `send-heredoc` writes a message request.
2. The daemon validates the route from `edges`.
3. Valid mail is moved into `inbox/{node}/`.
4. The recipient pane receives `notification_template`.
5. The recipient claims and archives the message with `pop`; `pop` returns
   metadata plus the archived message/body path instead of pushing the full
   body into the pane hint.
6. The recipient reads the complete archived Markdown body before any handling,
   routing, reply, status decision, or no-action or no-op decision. This
   applies to daemon PING mail, `messageType: ping`, `replyPolicy: none`, and
   every other message type.

Unroutable mail goes to `dead-letter/`. Dead-letter handling embeds its own
manual recovery guidance and is separate from normal pane hints. The durable
dead-letter journal event preserves the original message ID, sender, recipient,
source `post/` path, dead-letter path, failure reason, and any exact
input-request identifiers parsed from the message metadata.

## 3. Status Model

get-status, get-status-oneline, and the default TUI are three views over the
same canonical contract.

| State     | Meaning                                       | Compact mark      |
| --------- | --------------------------------------------- | ----------------- |
| `initial` | No positive live evidence has arrived yet     | `⚫` black circle  |
| `ready`   | Pane is live with no open action or wait      | `🟢` green mark    |
| `waiting` | Node is waiting for a reply-required response | `🟡` yellow mark   |
| `pending` | Node has inbound reply-required action        | `🔷` blue diamond  |
| `stale`   | Previously known pane/session is stale        | `🔴` red mark      |

A live pane that simply has not changed for a long time is internally `idle`
and remains `ready` in the visible status model.

`initial` is neutral. Non-AI panes, unreachable or unclassified sessions, and
configured or expected AI panes with no positive response or activity remain
`initial` until evidence moves them to `ready`, `waiting`, `pending`, or
`stale`.

Session fallback may report `unavailable` when this daemon cannot provide
canonical status for a tmux session. It is displayed with the neutral `⚫`
mark, but it is not a per-node state.

The status payload exposes `queues.post_count`, `queues.inbox_count`,
`queues.dead_letter_count`, and per-node input-request counts for mailbox
backlog checks. Per-node state is reported as `nodes[*].visible_state`.

The schema version 4 payload also exposes contextual severity:
`severity`, `severity_source`, `severity_reason`, `compact_severity`,
`delivery`, `nodes[*].node_local`, `nodes[*].flow`, and `nodes[*].queues`.
These fields distinguish expected waits from actionable or broken conditions
without changing the visible-state fields.

Replay keeps a narrow read-only reader for pre-v4 `session_health_snapshot`
archives, but new writers and live machine consumers use status terminology.
See [Schema and Event Terminology](schema-event-terminology.md).

| Severity             | Meaning                                           |
| -------------------- | ------------------------------------------------- |
| `ok`                 | No open action, wait, local work, or delivery bug |
| `working`            | Local pane activity or queued delivery is present |
| `expected_wait`      | Waiting for an expected required reply            |
| `needs_action`       | Inbound required reply is open                    |
| `blocked`            | Open blocked report exists                        |
| `attention_stale`    | Pane or session evidence is stale or unavailable  |
| `delivery_stuck`     | Pending post delivery is at least 180 seconds old |
| `delivery_failure`   | Dead-letter delivery failure exists               |

`get-status-oneline` keeps compact visible-state marks by default. Use its
`--severity` flag when the operator needs an ASCII severity token. Tokens with
`?` are inferred, for example a `BLOCKED:` first line without structured
blocked-report metadata.

## 4. Configuration

The remaining notification-related public settings are:

| Field                      | Purpose                                                     |
| -------------------------- | ----------------------------------------------------------- |
| `notification_template`    | Pane hint rendered when mail arrives; not full message body |
| `message_footer`           | Reply guidance rendered before the sender body separator    |
| `draft_template`           | Structured envelope for stored `send-heredoc` Markdown      |
| `daemon_message_template`  | Structured envelope for daemon-originated startup PING      |
| `ui_node`                  | Optional target filter for startup auto-PING                |
| `auto_enable_new_sessions` | Auto-enable sessions with configured node panes             |

Stored message Markdown is an envelope. The default `send-heredoc` template
keeps recipient instructions, reply guidance, and sender-authored content
separate. Generated transport/header content appears first under
`Recipient Instructions` and `Sender Message`; then a visible `---` separator
introduces the original sender body. Sender body Markdown is inserted verbatim
after that separator, so headings such as `#` and `##` are not demoted or
rewritten. Recipient role instructions are still demoted when inserted into the
generated envelope so they stay visually inside `Recipient Instructions`.
The default message footer and daemon PING template render `You can talk to:`
as a concise bullet list of adjacent nodes. When a node has `postman.md` or
TOML `role` text, only the first non-empty role line is shown as the summary;
full node templates are not dumped into the contact list. Custom footers can
keep using `{can_talk_to}` for the legacy comma-separated node list or
`{contacts_section}` for the role-aware Markdown list.
Daemon PING mail uses the same generated-envelope pattern with
`Recipient Instructions` and `Daemon Message`.

Pane notifications are intentionally not a body delivery surface. The default
notification says to run `tmux-a2a-postman pop` to claim the message and get the
archived body path. This preserves the receiver-owned mailbox state transition:
the daemon may hint that mail arrived, but the receiver decides when to pop the
inbox item and open the referenced body.

Consumers must not make handling, routing, reply, status, no-action, or no-op
decisions from metadata-only inspection. After `pop` returns `status=message`,
the archived Markdown body is the authoritative instruction surface. If a
runtime reads it through bounded stdout, it must detect truncation and continue
with verified chunks through EOF; truncated output is not a complete body read.

No separate claim/open alias exists today. The command name `pop` remains the
canonical state-machine operation; the user-facing wording and `pop` JSON
fields carry the clearer claim/open/message-file semantics.

`ui_node` is not a general escalation channel. It is normally set by marking a
node in the `postman.md` Mermaid graph with `class <node> ui_node`; inline
`:::ui_node`, frontmatter, and TOML remain explicit override surfaces. When
empty, startup auto-PING may target all discovered nodes. When set, startup
auto-PING is limited to that node if it is discovered in an enabled session.
`auto_enable_new_sessions` defaults to true, so a single user daemon can
discover project sessions that already have configured node panes.
