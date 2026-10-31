# Mail-derived node state

Unread mail, inbound replies owed and outbound replies awaited are independent
file-derived facts. POP clears unread attention, never a reply obligation.
An exact reply closes only its own required input; it does not accept task
completion. Each send and POP, including an empty POP, returns self-state.

Missing, conflicting, damaged or stale evidence is unknown, never fabricated
zero work. Optional bounded pane observations carry timestamps and runtime
identity; identical captures cannot establish ongoing activity or inactivity.

`get-status` reads saved files without registration, POP, capture or implicit
initialization. It does not schedule follow-ups. No daemon state machine or
resident cache is involved. See [architecture](file-first-architecture.md).
