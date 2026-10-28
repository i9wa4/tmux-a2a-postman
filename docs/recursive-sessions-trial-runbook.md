# Independent Recursive Sessions: Validation

This is the issue #937 validation contract for a declared parent and
child interface hierarchy. The no-daemon path is still under review;
a paid-model trial is optional and cannot replace deterministic
filesystem and disposable tmux tests.

## 1. Deterministic proof

Use a private HOME and XDG state root, an isolated tmux server socket,
and explicitly configured parent and child workspaces. Give same-named
roles distinct native bindings to catch accidental local fallback.
The fixture must verify:

1. A parent interface sends to a direct child interface and the child
   replies through the authorized return edge; non-relatives fail.
2. Receiver inbox, sender sent, final read path, exact reply request,
   and remaining unread obligations survive independent process calls.
3. A second live session cannot take over an owned workspace; verified
   stale takeover retains history; unknown ownership fails closed.
4. The CLI returns after its own bounded delivery/notice attempt and
   never waits for the recipient to POP or reply.
5. A genuinely supported recipient cue, if available, proves the exact
   committed ID was accepted and settled before claiming `sent`.
   Pending input, stale UI, and a rebound pane fail closed.
6. Cleanup stops only the fixture's tmux server and removes only its
   temporary state. No user mailbox or active pane is modified.

The repository process and native tmux tests provide part of this
coverage. Do not count a fake app receipt as a positive supported
recipient UI transition, or current partial coverage as criterion #17.

## 2. Optional real-agent trial

A human may opt into a later trial using a separate tmux server,
private state root, fixed workload, and a time limit. Record the
binary commit and version, each agent runtime and model, topology,
role instructions, exact message IDs, final read paths, response
obligations, interventions, and latency. Use `unavailable` for missing
measurements. Do not run the trial against work sessions or treat a
single model run as proof of infrastructure correctness.

Report deterministic test results separately from agent behavior.
An agent's `DONE` or reviewer's `PASS` is a claim to check against
saved mail and the original acceptance criteria.
