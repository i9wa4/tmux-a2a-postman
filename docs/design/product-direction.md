# Product Direction

tmux-a2a-postman is becoming a simple session runtime for agent teams in tmux.

The message lane remains the center:

- `send`
- `pop` and `read`
- routing and reply flow
- dead-letter handling

Around that core, the product is gaining runtime surfaces:

- canonical health and alerts
- replay and projection views
- local runtime context on approved read paths
- explicit summary surfaces such as TODO status

It is not becoming:

- a dashboard-first control plane
- a generic workflow engine
- a catch-all brand for unrelated agent tools

The external name stays for now because it still matches the operator loop,
and the broader runtime shape is not settled enough to justify rename churn.

Internal terminology should become more precise:

- `message lane`: stored-message delivery and reading
- `session runtime`: live health, alerts, projections, and other session state
- `runtime surface`: explicit operator views outside stored messages

Design rule: keep stored messages simple and durable. Add runtime surfaces
only when they explain the session without hiding the message lane.

Rename discussion can reopen later, but only after several non-message
surfaces stabilize into one coherent product.

Until then, tighten vocabulary, ship simple surfaces, and keep the name stable.
