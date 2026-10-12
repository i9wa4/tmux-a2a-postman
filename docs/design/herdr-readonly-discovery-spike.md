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

### 3.1. Response Identity Binding

A response that decodes cleanly can still describe a different target than the
one requested. The backend therefore binds every consumed `pane.read` and
`pane.process_info` response to its request before returning any content:

- `pane.read` must echo the requested `pane_id`, the configured workspace and
  tab, and the `source` actually sent (`visible` by default, `recent` for
  history or tail captures);
- `pane.process_info` must echo the requested `pane_id`;
- comparison is byte-exact, with no trimming or case folding;
- an identity field that is missing, null, or not a string is a malformed
  response and fails as `ErrHerdrBackendUnavailable`; a field that is present
  but empty or different fails as `ErrHerdrResponseTargetMismatch`;
- `ErrHerdrResponseTargetMismatch` is distinct from
  `ErrHerdrPaneTargetMismatch`, which rejects the caller's own pane argument
  before any request is sent;
- the mismatch error carries only escaped, length-bounded identities, and the
  mismatched text or command is never returned, logged, or cached;
- the discovery process probe uses the same binding: a wrong or empty
  `process_info.pane_id` aborts `Discover` with the mismatch error instead of
  quietly marking the pane stale.

Residual: `pane.process_info` carries no workspace or tab, so the workspace and
tab binding of `PaneCurrentCommand` rests on the containment snapshot read just
before the request. A pane that moves between that snapshot and the process
read is not detected. This time-of-check to time-of-use window is a recorded
residual; the JSON-RPC response ID check still rejects a response that does not
belong to the request.

## 4. Compatibility Authority

The compatibility source of truth is the protocol/schema pair observed from the
Herdr server reached through `HERDR_SOCKET_PATH` and corroborated by the
`herdr api schema --json` command visible on `PATH`. Postman does not treat
either source as authoritative by itself:

- `ping` must return the supported socket protocol before any response fields
  are consumed;
- `herdr api schema --json` must return the same protocol and a supported
  schema version, but its executable path and release string are not compared to
  the socket server process;
- unsupported, missing, zero, unparsable, or same-protocol-but-newer schema
  evidence fails closed as `ErrHerdrBackendUnavailable`;
- the multiplexer read gate still validates the negotiated envelope against
  local allowlists before discovery, capture, or process information is used.

The supported production compatibility boundary is socket protocol `20` with
schema version `1`. The server release string from `ping` must be nonempty so
operators have provenance evidence, but it is informational and is not part of
the runtime compatibility decision. Expanding either protocol or schema value
requires updating this design record, the socket-client compatibility constants,
and the socket harness regression tests together.

## 5. Identity Mapping

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

## 6. Status Projection

Herdr layout projects to backend-neutral `SessionLayout` groups with
`kind: tab`, Herdr workspace/tab/pane native IDs, and `backend: herdr` resource
IDs. tmux `windows` are marked unsupported in native evidence; first-phase
Herdr status should use `layout_groups` as the authoritative structural shape.

## 7. External References

- Herdr socket API: <https://herdr.dev/docs/socket-api/>
- Herdr session state and restore: <https://herdr.dev/docs/session-state/>
