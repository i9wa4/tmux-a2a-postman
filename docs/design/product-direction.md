# Product Direction

tmux-a2a-postman coordinates agents through durable, private workspace
mail and four foreground commands: `send-heredoc`, `pop`,
`get-status`, and `execute-bash`. Issue #937's file-first cutover is
under review; the target requires no resident Postman daemon for
those commands.

The sender commits a receiver inbox copy and its own sent copy.
The receiver claims its mail with `pop`. Saved files carry unread
and exact reply obligations across restarts. A bounded pane
observation may explain activity, but it cannot become mailbox truth
or justify a claim that an agent is working.

A verified native workspace binding limits who can act as a team.
Direct parent and child interface edges allow independent tmux
sessions to exchange authorized mail without one supervisor. The
CLI does not launch agents. If a launcher is added later, it can
reuse the ownership check.

The command-approval path is a coordination and audit rail, not an
OS sandbox. It uses an exact correlated decision when an approver is
present and advisory execution only when absence is positively
verified. See [command approvals](../command-approvals.md).

Compaction detection remains reusable for a future optional observer.
The product does not need a TUI, startup PING, queued delivery worker,
or long-lived state machine to operate. Keep the implementation and
documentation small enough to inspect and recover. The
[architecture](file-first-architecture.md) records the cutover gates.
