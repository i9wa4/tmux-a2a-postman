# Recursive Sessions: Validation Layers and Opt-In Trial Runbook

Refs #768. This document separates two validation layers for the
personal-organization model (top-level, owner/group, and repository sessions
that each use the same role template with one mouthpiece) and gives the
runbook for the second, opt-in layer.

## 1. Two Layers, Two Kinds of Evidence

| Layer                   | Measures                        | Needs a model | Where it lives                           |
| ----------------------- | ------------------------------- | ------------- | ---------------------------------------- |
| Deterministic scenario  | Postman transport and state     | No            | `go test` (see section 2)                |
| Opt-in real-agent trial | Whether agents follow the rules | Yes           | This runbook (section 3), run by a human |

A deterministic pass says the infrastructure routed and recorded correctly.
It says nothing about whether real agents behave. A trial result says how
agents behaved on one workload at one configuration. Never report a mocked
success as proof of multi-agent reliability, and never report a trial as proof
of infrastructure correctness.

## 2. Deterministic Coverage Today

`internal/cli/tree_alias_exchange_test.go` uses the real producer
(`send-heredoc --to <alias>`) and the real delivery consumer
(`message.DeliverMessage`) across three registered levels. All three sessions
use the same node name and the same template shape; only the session name,
`workspace_tree` label and parent, and the edges differ.

Covered:

- `@parent`, `@parent/<node>`, `@child/<label>`, `@child/<session>`,
  `@child/<node>` (single child), and `@child/<label>/<node>` compile to the
  declared relative and are delivered with exact `from`/`to`.
- Non-relatives (the root's parent, a grandchild, an unknown child, a leaf's
  child, a malformed alias) are rejected with no draft left behind.
- A four-hop request-down/result-up exchange stays inside the expected
  inboxes: no cross-delivery between same-named mouthpieces and no
  dead-letters.

Not covered yet (explicitly open, not counted as passes):

- Scenarios that depend on `interface_node` (#764 slice A, #890), on
  authorization (#624, #700), on hierarchical request/return linkage (#765),
  on status and decision reporting (#766), or on the external human channel
  (#767).
- Restart, timeout, late-response and duplicate-delivery recovery, reconnect,
  and loop-bound scenarios.
- Anything involving a live daemon or real panes.

## 3. Opt-In Real-Agent Trial

Run this only with an explicit decision to spend model time. Do not run it
against the daemon or panes you use for real work, and do not write to any
production or public surface as part of the trial.

### 3.1. Provenance to record before starting

Record each item; write `unavailable` rather than leaving a gap.

- Date, operator, and the repository commit under test
  (`git rev-parse HEAD`) plus `tmux-a2a-postman --version`.
- Exact runtime and model for every participant (for example the Claude Code
  or Codex version and model id).
- Role template and skill configuration revision (commit or content hash of
  the template and `skills/` used by every session).
- The `workspace_tree` configuration and edges (redacted copy).
- The workload: the literal request given at the top-level mouthpiece and its
  written acceptance criteria, fixed before the run.

### 3.2. Procedure

1. Start an isolated Postman context with three sessions registered as in
   section 2. Use a scratch repository, not a real project.
2. Give the workload to the top-level mouthpiece only. Do not message lower
   levels directly; if you must, that is an intervention (section 3.3).
3. Let the agents run to a result or to a decision request. Answer decision
   requests through the top-level mouthpiece only.
4. Stop at the first of: a returned result, an unresolved request that no
   agent is working, or the time limit you set in advance.
5. Collect the evidence bundle (section 3.4) before cleaning anything up.
   Do not repair state by hand to make the report look better.

### 3.3. Scoreboard

Record each outcome from independently checkable evidence, not from an
agent's DONE or a reviewer's PASS:

- Accepted tasks verified against the original criteria; false completion
  reports (an agent said done but the criteria are not met).
- Unresolved requests left open, duplicate handling, misrouted responses,
  recovery inconsistencies.
- Human interventions and lower-level pane visits needed to finish. The
  happy-path target is zero visits; record every unexpected one.
- End-to-end latency and decision-return latency.
- Token and cost data where the runtime reports them; otherwise mark
  `unavailable`.

### 3.4. Redacted evidence bundle

Keep enough to replay or audit the result, with secrets, private paths, and
private message content removed:

- The provenance record from section 3.1.
- Per-session `get-status` output at start and end.
- The message ids and filenames of every hop (not the bodies unless
  non-sensitive).
- Relevant `inspect-message` and dead-letter listings.
- The scoreboard and a short note of each intervention and why it was needed.

### 3.5. Reporting limits

State the evidence limits in every report: single workload, runtime and model
versions, whether any layer was mocked, which scenarios in section 2 were not
exercised, and anything left `unavailable`. A trial that required manual
state repair is reported as incomplete, not as a pass.
