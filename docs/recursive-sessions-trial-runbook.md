# Recursive Sessions: Validation Layers and Opt-In Trial Outline

Refs #768. This document separates two validation layers for the
personal-organization model (top-level, owner/group, and repository sessions
that each use the same node name and role text) and gives an OUTLINE for the
second, opt-in layer. The outline is not yet an executable procedure; see
section 3.

## 1. Two Layers, Two Kinds of Evidence

| Layer                   | Measures                        | Needs a model | Where it lives                           |
| ----------------------- | ------------------------------- | ------------- | ---------------------------------------- |
| Deterministic scenario  | Postman transport and state     | No            | `go test` (see section 2)                |
| Opt-in real-agent trial | Whether agents follow the rules | Yes           | This outline (section 3), run by a human |

A deterministic pass says the infrastructure routed and recorded correctly.
It says nothing about whether real agents behave. A trial result says how
agents behaved on one workload at one configuration. Never report a mocked
success as proof of multi-agent reliability, and never report a trial as proof
of infrastructure correctness.

## 2. Deterministic Coverage Today

`internal/cli/tree_alias_exchange_test.go` uses the real producer
(`send-heredoc --to <alias>`) and the real delivery consumer
(`message.DeliverMessage`) across three registered levels. All three sessions
use the same node name and the same role text; only the session name,
`workspace_tree` label and parent, and the edges differ.

Covered:

- `@parent`, `@parent/<node>`, `@child/<label>`, `@child/<session>`,
  `@child/<node>` (single child), and `@child/<label>/<node>` compile to the
  declared relative and are delivered with exact `from`/`to`; no other inbox,
  post, draft, or dead-letter directory in any session holds anything.
- Non-relatives (the root's parent, a grandchild, an unknown child, a leaf's
  child) are rejected by the tree with the alias-layer reason, a malformed
  alias with the alias syntax error, and the same holds when a direct
  top-to-repo edge exists, so the tree alone refuses the send. A rejected send
  leaves nothing behind in any session.
- Four independent directed alias sends (down, down, up, up) stay inside the
  expected inboxes: no cross-delivery between same-named nodes and no
  dead-letters.

Not covered yet (explicitly open, not counted as passes):

- Reply-slot return routing: the four sends above are plain sends with no
  pop, `--reply-required`, `--reply-to`, or `--fills-input-request-id`
  (hierarchical request/return linkage belongs to #765).
- Scenarios that depend on `interface_node` (#764 slice A, #890), on
  authorization (#624, #700), on status and decision reporting (#766), or on
  the external human channel (#767).
- Restart, timeout, late-response and duplicate-delivery recovery, reconnect,
  and loop-bound scenarios.
- Anything involving a live daemon or real panes.

## 3. Opt-In Real-Agent Trial (Outline Only)

This section is an outline of what a trial must record and report. It is NOT
a runnable procedure: the concrete, verified setup is later work. A runnable
version must first specify, and verify against `tmux-a2a-postman help` on the
version under test, at least:

- the configuration with `workspace_tree` and edges for three sessions;
- a Postman base directory and tmux socket or namespace that are separate from
  any daemon and panes used for real work;
- the commands to start, inspect, and tear the trial down.

Until that procedure exists and is verified, do not run a trial against the
daemon or panes you use for real work, and do not write to any production or
public surface as part of a trial.

### 3.1. Provenance to record

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

### 3.2. Shape of a run

1. Give the workload to the top-level mouthpiece only. Contacting a lower
   level directly is an intervention (section 3.3).
2. Answer decision requests through the top-level mouthpiece only.
3. Stop at the first of: a returned result, an unresolved request that no
   agent is working, or a time limit set in advance.
4. Collect the evidence bundle (section 3.4) before cleaning anything up. Do
   not repair state by hand to make the report look better.

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
