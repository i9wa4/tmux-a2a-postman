# Daemon/Session Model

Design document clarifying the intended daemon/session architecture.

## Four Principles

### 1. Daemon Location Independence

A daemon can run from ANY tmux pane or session. There is no requirement for the
daemon to reside in the same tmux session as the agent nodes it serves.

### 2. Multiple Daemons Allowed

Multiple daemons may run simultaneously. Startup rejects only a duplicate
daemon for the same `contextID` and tmux session; daemons in other contexts or
other tmux sessions remain allowed. The system places no hard limit on the
number of running daemon processes.

### 3. Exclusive Session Ownership

Only ONE daemon may have a given tmux session set to ON at a time. This
constraint is not a global startup ban across all daemons. Instead, the daemon
blocks later ownership collisions when a session is enabled, so only one live
daemon may actively own a given tmux session at a time. The simplified default
TUI no longer serves as the ownership-transfer control surface.

### 4. Cross-Daemon Node Discovery

Nodes in a tmux session are discoverable by any daemon regardless of where that
daemon runs from. Discovery is based on tmux pane metadata, not daemon locality.

## Design Intent

These principles correct stale documentation that implied a same-session
constraint between daemon and agent nodes. The daemon is a routing process that
reads tmux state; it has no topological dependency on the session it observes.
