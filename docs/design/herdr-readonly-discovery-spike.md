# Herdr Read-Only Discovery Spike

Issue: #658.

## 1. Purpose

This spike adds a disabled-by-default Herdr backend boundary for read-only
discovery, pane capture, process information, and layout/status projection.
tmux remains the default and only automatically selected runtime backend.

Herdr write/mutation behavior remains out of scope for #658. Do not send input,
create/split/move/resize/close panes, mutate Herdr metadata, or write ownership
markers through Herdr in this phase.

## 2. Activation

The zero-value Herdr read configuration is disabled. A caller must explicitly
construct `multiplexer.HerdrReadConfig{Enabled: true, ...}` with:

- `HERDR_SOCKET_PATH`;
- Herdr named session;
- Herdr workspace ID;
- socket/session/workspace allowlists;
- allowed protocol and schema versions.

`multiplexer.NewHerdrBackend` refuses disabled configuration and missing
clients. Every read path calls the #660 read gate against local runtime policy
before making any Herdr client call, then validates each returned response
envelope before consuming Herdr response fields. Discovery reads use
`HerdrReadScopeDiscovery`; pane capture and process info use
`HerdrReadScopePane`.

## 3. Read APIs

The backend accepts an injected read-only client with these operations:

- `ping` for optional read-only availability probes;
- `session.snapshot` for workspace/tab/pane discovery;
- `pane.read` for capture;
- `pane.process_info` for current-command evidence.

Unavailable socket/server failures are normalized to
`ErrHerdrBackendUnavailable` so higher layers can treat missing Herdr like an
unavailable backend instead of a tmux failure.

## 4. Identity Mapping

The public postman address remains `session:node`.

For #658:

- postman session maps to an explicitly configured Herdr named session plus an
  allowlisted workspace ID;
- postman node maps to postman-owned metadata or launch environment attached to
  Herdr panes;
- pane labels, terminal titles, process info, and cwd are advisory evidence
  only;
- duplicate node claims become explicit `HerdrIdentityCollision` records;
- stale pane evidence is reported as backend-native Herdr pane resource IDs.

## 5. Status Projection

Herdr layout projects to backend-neutral `SessionLayout` groups with
`kind: tab`, Herdr workspace/tab/pane native IDs, and `backend: herdr` resource
IDs. tmux `windows` are marked unsupported in native evidence; first-phase
Herdr status should use `layout_groups` as the authoritative structural shape.

## 6. External References

- Herdr socket API: <https://herdr.dev/docs/socket-api/>
- Herdr session state and restore: <https://herdr.dev/docs/session-state/>
