# Product direction

Postman coordinates agents using readable durable mail and short foreground
commands. A sender owns publication and a bounded notification attempt; the
recipient owns POP, full reading and response. No permanent Postman process,
daemon startup, RPC, retry queue or implicit task scheduler is required.

The [architecture](file-first-architecture.md) owns storage/identity contracts.
[Notification](notification.md) owns recipient activation, and
[command approvals](../command-approvals.md) owns the bounded approval wrapper.
Keep one canonical contract rather than parallel operational workflows.

[Agent Runtime Feature Differences](../agent-runtime-feature-differences.md)
records which behavior belongs to tmux-a2a-postman and which behavior belongs
to Claude Code or Codex CLI. Future observers are a separate decision.
