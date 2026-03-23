# Daemon/Session Model

Design document clarifying the intended daemon/session architecture.

## Four Principles

### 1. Daemon Location Independence

A daemon can run from ANY tmux pane or session. There is no requirement for the
daemon to reside in the same tmux session as the agent nodes it serves.

### 2. Multiple Daemons Allowed

N daemons may run simultaneously with no restrictions at startup. The system
places no hard limit on the number of running daemon processes.

### 3. Exclusive Session Ownership (Toggle-Only Enforcement)

Only ONE daemon may have a given tmux session set to ON at a time. This
constraint is enforced solely via the TUI Space-key toggle — it is NOT enforced
at daemon startup. Operators must use the TUI to transfer session ownership
between daemons.

### 4. Cross-Daemon Node Discovery

Nodes in a tmux session are discoverable by any daemon regardless of where that
daemon runs from. Discovery is based on tmux pane metadata, not daemon locality.

## Design Intent

These principles correct stale documentation that implied a same-session
constraint between daemon and agent nodes. The daemon is a routing process that
reads tmux state; it has no topological dependency on the session it observes.
