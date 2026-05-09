# Daemon/Session Model

Design document clarifying the intended daemon/session architecture.

## Four Principles

### 1. Daemon Location Independence

A daemon can run from ANY tmux pane or session. There is no requirement for the
daemon to reside in the same tmux session as the agent nodes it serves.

### 2. Single-Daemon Operator Model

The default operator workflow assumes one daemon process per Unix user. Startup
first rejects any already-live `postman.pid` owned by the current user anywhere
under the configured base directory, then acquires a current-user-wide process
lock so concurrent starts cannot race into two daemons. A different Unix user's
daemon is still treated as alive for cleanup safety, but it is not treated as
the current user's owner.

### 3. Exclusive Session Ownership

Only ONE daemon may have a given tmux session set to ON at a time. This
constraint is not just a later enable-time guard. The per-user startup lock is
the first process-level guard, the session-wide startup lock remains an
additional same-session guard, and later ownership checks still block collisions
when a session is enabled, so only one live daemon may actively own a given tmux
session at a time. The simplified default TUI no longer serves as the
ownership-transfer control surface.

Cross-context ownership follows the live enabled-session marker, not leftover
session directories. A foreign context counts as owner only when its daemon is
still live, is owned by the current Unix user, and the session's
`@a2a_session_on_<session>` marker names that context. The daemon's own tmux
session still counts as owned while it is running, even before any later
cross-session discovery.

### 4. Cross-Daemon Node Discovery

Nodes in a tmux session are discoverable by any daemon regardless of where that
daemon runs from. Discovery is based on tmux pane metadata, not daemon locality.

## Design Intent

These principles correct stale documentation that implied a same-session
constraint between daemon and agent nodes while keeping the default operator
story simple: run one daemon for your Unix user and let it observe the sessions
you enable. The daemon is still a routing process that reads tmux state; it has
no topological dependency on the session it observes.

For the current inventory of daemon-side tmux command execution and polling
cadence, see
[Daemon tmux Command Inventory](./daemon-tmux-command-inventory.md).
