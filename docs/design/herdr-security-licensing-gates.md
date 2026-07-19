# Herdr Security And Licensing Gates

Issue: #660.

## 1. Purpose

Herdr support remains inactive until explicit security and compliance gates
pass. This document records the gate policy that #658 and #659 must use before
adding read-only Herdr discovery/capture/status or Herdr write/mutation
behavior.

Current Herdr references reviewed for this gate:

- <https://github.com/ogulcancelik/herdr>
- <https://herdr.dev/docs/socket-api/>
- <https://herdr.dev/docs/cli-reference/>
- <https://herdr.dev/docs/session-state/>

## 2. Capability Split

Herdr reads and writes are separate capabilities.

Read capabilities include:

- opening or selecting `HERDR_SOCKET_PATH`;
- discovery reads such as `session.snapshot` and workspace/tab/pane layout
  enumeration;
- pane-targeted reads such as CLI or socket APIs like `pane.read`;
- consuming workspace, tab, pane, layout, metadata, or process information after
  the matching read scope passes.

Write capabilities include:

- sending interactive input, keys, or text to a pane;
- launching, splitting, moving, resizing, or closing panes;
- changing workspace, tab, pane, metadata, labels, layout, or focus;
- writing postman ownership markers into Herdr-owned state.

Issue #658 may add only read behavior after the read gate passes. Issue #659
may add write behavior only after #658 validates the read path and the write
gate passes.

## 3. Mechanical Gate

`multiplexer.ValidateHerdrReadGate` and
`multiplexer.ValidateHerdrWriteGate` are the code-level preflight guards for
future Herdr paths.

The read gate fails closed unless all are true:

- read access is explicitly enabled by policy;
- `HERDR_SOCKET_PATH`, Herdr named session, and workspace ID are present in the
  runtime identity;
- socket path, named session, and workspace ID match explicit allowlists;
- the response protocol version is supported before response fields are trusted;
- the response schema version is supported before response fields are trusted.

Discovery reads use the default `multiplexer.HerdrReadScopeDiscovery` scope and
must not require tab or pane IDs before #658 can discover them. Pane-targeted
reads use `multiplexer.HerdrReadScopePane` and additionally require tab ID and
pane ID before consuming pane content or pane metadata.

The write gate composes the pane-targeted read gate and also requires:

- write access is explicitly enabled by policy;
- a Herdr-safe interactive input sanitization path is ready;
- compliance has been resolved as either AGPL-3.0-or-later compatible or
  commercial-license compatible.

`review-only` compliance status is not enough for writes. It may be used in
planning artifacts to record that legal/compliance review is still pending.

## 4. Allowlists

Allowlists must be exact matches, not pattern or prefix checks:

- socket path: exact `HERDR_SOCKET_PATH`;
- session: exact Herdr named session selected by config or launch environment;
- workspace: exact Herdr workspace ID.

Tab and pane IDs are required only for pane-targeted reads and writes. They are
not global allowlist roots. They must be scoped under an allowlisted
session/workspace and validated against `session.snapshot` before use.

If multiple Herdr panes advertise the same postman node name, #658 must report a
collision instead of silently selecting a pane.

## 5. Protocol And Schema Versions

Herdr responses must include explicit protocol and schema version evidence
before postman consumes response fields. Unsupported, missing, zero, or
unparseable versions are gate failures.

Issue #658 must define the first accepted Herdr protocol/schema version values
from the concrete response shape it consumes. Issue #659 must reuse the same
policy and extend it only when write response schemas require additional
versions.

## 6. Unavailable Backend Normalization

Absent Herdr server, missing socket, refused socket connection, unsupported
protocol, unsupported schema, and unauthorized session/workspace are not tmux
errors. Herdr code must normalize them at the backend boundary before higher
layers see them.

Expected first-phase behavior:

- read-only status/discovery treats no Herdr server or no socket like an
  unavailable backend, not as a corrupted session;
- write paths fail closed and leave filesystem mail/projection state unchanged
  when the backend is unavailable or unauthorized;
- error messages identify the failed Herdr gate field without exposing secrets
  from socket paths, metadata, process info, or pane content.

## 7. Session And Identity Mapping

The external postman address remains `session:node`.

Herdr mapping for future implementation:

- postman session maps to a configured logical session name plus an allowlisted
  Herdr named session and workspace ID;
- postman node maps to postman-owned metadata or launch environment in a Herdr
  pane;
- Herdr pane labels are display/fallback evidence only, not authoritative
  identity;
- backend-native resource evidence records Herdr workspace ID, tab ID, pane ID,
  named session, and socket path source, with secret and path redaction where
  status or runtime-context output might expose local details.

## 8. Input Sanitization

Herdr interactive delivery must preserve the security intent of the tmux
`notification.SendToPane` path:

- wrap postman-delivered pane input in protocol sentinels;
- strip VT/ANSI control sequences and invalid UTF-8 before write APIs receive
  text;
- keep key-combo APIs and text-input APIs separate;
- never pass untrusted body text as key-combo syntax.

Issue #659 implements this path behind explicit registration. Herdr write
configuration still must set `InputSanitizerReady`; otherwise
`ValidateHerdrWriteGate` returns `sanitizer_missing` before write or mutation
RPCs are issued.

## 9. Licensing And Compliance

As of the #660 check, Herdr's public repository states a dual license:

- open source: GNU Affero General Public License v3.0 or later
  (`AGPL-3.0-or-later`);
- commercial licenses for organizations that cannot comply with AGPL.

Before distribution, generated-protocol reuse, vendoring, linking, embedding, or
Herdr write/mutation support, record one of:

- `agpl-3.0-or-later`: the integration shape is AGPL-compatible;
- `commercial`: a commercial license covers the integration shape;
- `review-only`: no distributable/generated/write integration may be shipped.

CLI or socket use alone does not resolve licensing obligations. A future issue
must record the exact integration shape before changing dependency, vendoring,
generated code, or distribution behavior.

## 10. Out Of Scope

Issue #660 does not implement Herdr discovery, capture, status, interactive
delivery, ownership mutation, socket clients, generated protocol clients, or
packaging changes.
