# Daemon/Session Model

Design document clarifying the intended daemon/session architecture.

## Four Principles

### 1. Daemon Location Independence

A daemon can run from ANY tmux pane or session. There is no requirement for the
daemon to reside in the same tmux session as the agent nodes it serves.

### 2. Multiple Daemons Allowed

Multiple daemons may run simultaneously, but startup is still serialized per
tmux session name. The start path first rejects a duplicate daemon for the
same `contextID` plus tmux session via `postman.pid`, then acquires a
tmux-session-wide lock, so two contexts cannot start daemons against the same
tmux session at the same time. Daemons in other tmux sessions remain allowed.

### 3. Exclusive Session Ownership

Only ONE daemon may have a given tmux session set to ON at a time. This
constraint is not just a later enable-time guard. The session-wide startup lock
is an additional same-session guard, and later ownership checks still block
collisions when a session is enabled, so only one live daemon may actively own
a given tmux session at a time. The simplified default TUI no longer serves as
the ownership-transfer control surface.

Cross-context ownership follows the live enabled-session marker, not leftover
session directories. A foreign context counts as owner only when its daemon is
still live and the session's `@a2a_session_on_<session>` marker names that
context. The daemon's own tmux session still counts as owned while it is
running, even before any later cross-session discovery.

### 4. Cross-Daemon Node Discovery

Nodes in a tmux session are discoverable by any daemon regardless of where that
daemon runs from. Discovery is based on tmux pane metadata, not daemon locality.

## Design Intent

These principles correct stale documentation that implied a same-session
constraint between daemon and agent nodes. The daemon is a routing process that
reads tmux state; it has no topological dependency on the session it observes.
