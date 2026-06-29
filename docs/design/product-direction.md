# Product Direction

tmux-a2a-postman is becoming a simple session runtime for agent teams in tmux:
a thin but organic coordination layer around existing agents and terminal
processes.

It should connect those processes lightly from the outside. Mailbox threads,
reply-required approval threads, journal/projection state, and tmux pane state
give operators coordination, auditability, approval flow, and continuity
without requiring postman to own the agent harness.

For project-wide decision rules, see
[Project Design Philosophy](project-design-philosophy.md).

The message lane remains the center:

- `send-heredoc`
- `pop`
- routing and reply flow
- dead-letter handling

Around that core, the product is gaining runtime surfaces:

- canonical status JSON
- compact all-session status
- a default TUI over the same status contract

Runtime surfaces must stay explicit about which behavior belongs to
tmux-a2a-postman and which behavior belongs to Claude Code or Codex CLI. The
canonical comparison is
[Agent Runtime Feature Differences](../agent-runtime-feature-differences.md).
Postman may add backend-independent command approval and audit rails, but it
should not claim OS-level sandboxing or total command enforcement. Integrated
runtimes such as Omnigent may catch up on enforcement and UX; postman's durable
differentiators are transparent local state, local auditability, operational
simplicity, and cross-agent or cross-backend use.

It is not becoming:

- a dashboard-first control plane
- a generic workflow engine
- a catch-all brand for unrelated agent tools
- a deeply integrated agent runtime or harness that owns model sessions,
  runners, sandboxing, and lifecycle policy

The external name stays for now because it still matches the operator loop,
and the broader runtime shape is not settled enough to justify rename churn.

Internal terminology should become more precise:

- `message lane`: stored-message delivery and reading
- `session runtime`: live status, projections, and other session state
- `runtime surface`: explicit operator views outside stored messages

Design rule: keep stored messages simple and durable. Add runtime surfaces
only when they explain the session without hiding the message lane.

Rename discussion can reopen later, but only after several non-message
surfaces stabilize into one coherent product.

Until then, tighten vocabulary, ship simple surfaces, and keep the name stable.
