# A2A Respect Design

This page defines how tmux-a2a-postman should respect the Agent2Agent (A2A)
Protocol vocabulary and model while staying honest about what it implements.

Respect means alignment, not compliance. Postman borrows A2A names and shape
where they clarify operator behavior, archives, and future migration. It does
not currently expose an A2A server, publish an AgentCard at
`.well-known/agent-card.json`, implement `SendMessage`/`GetTask` protocol
operations, negotiate `A2A-Version`, or guarantee A2A wire interoperability.

## 1. Sources Consulted

| Source                      | Version or date                   | URL                                                                               | Use in this page                                       |
| --------------------------- | --------------------------------- | --------------------------------------------------------------------------------- | ------------------------------------------------------ |
| A2A specification           | Latest released version 1.0.0     | <https://a2a-protocol.org/latest/specification/>                                  | Core model, TaskState, Message, Part, AgentCard        |
| A2A protobuf                | Tag `v1.0.0`, package `lf.a2a.v1` | <https://raw.githubusercontent.com/a2aproject/A2A/v1.0.0/specification/a2a.proto> | Normative field and enum names                         |
| A2A Life of a Task          | Accessed 2026-05-06               | <https://a2a-protocol.org/latest/topics/life-of-a-task/>                          | Context/task/message relationships and follow-ups      |
| A2A GitHub releases         | `v1.0.0`, 2026-03-12              | <https://github.com/a2aproject/A2A/releases>                                      | Current release and v1.0 migration notes               |
| tmux-a2a-postman issue #396 | Open, accessed 2026-05-06         | <https://github.com/i9wa4/tmux-a2a-postman/issues/396>                            | `pop` JSON, frontmatter, and `markdown_path` direction |

The A2A spec says the protocol has a layered model: a canonical data model,
abstract operations, and concrete protocol bindings. It also identifies the
protobuf file as the authoritative data-model source. Postman can respect the
data model and some operation names before it implements an A2A binding.

## 2. Respect Boundaries

Postman should use A2A terminology when it improves clarity:

- `contextId`/`context_id` for a group of related messages and tasks.
- `Message`/`message_id` for one communication unit.
- `Part` for message body content if #396 decomposes Markdown body access.
- `Artifact` as an analogy for durable outputs such as mkmd task artifacts.
- `TaskState` names when projecting postman states toward a broader protocol.
- `AgentCard` as a model for describing roles, skills, and capabilities.

Postman should avoid A2A claims that are not implemented:

- No statement that the daemon is an A2A-compliant server.
- No statement that `send-heredoc` is A2A `SendMessage`.
- No statement that `get-status` is A2A `GetTask`.
- No generated `task_id` unless the daemon owns actual task lifecycle state.
- No use of A2A terminal states for local tmux or delivery failures unless
  there is an explicit task abstraction to terminate.

The safe phrasing is: postman respects A2A v1.0 terminology and data-model
shape where useful. It remains a tmux/file-backed coordination runtime.

## 3. Relationship Model

A2A groups interaction with a `contextId`, may create server-owned `Task`
objects, and carries content in `Message.parts` or `Artifact.parts`.

```text
A2A v1.0

contextId
  Message messageId
    Part[]

contextId
  Task taskId
    status.state: TaskState
    history: Message[]
    artifacts: Artifact[]
      Part[]
```

Postman currently groups one daemon/session run with `contextId` in message
frontmatter and `context_id` in JSON, stores each message as a Markdown file,
and projects reply-required obligations as input requests.

```text
tmux-a2a-postman

contextId / context_id
  thread_id (optional workflow strand)
    message_id
      YAML frontmatter metadata
      Markdown body
      input_request_id (only when reply is required)
        filled later by fills_input_request_id

mkmd task artifact
  durable human-readable task plan, evidence, and result
```

Reply-required flow is the most important local lifecycle:

```text
sender sends reply-required message
  |
  +-- sender status: waiting_on_input
  |
  +-- recipient status: input_required
        |
        +-- recipient sends reply with fills_input_request_id
              |
              +-- sender waiting_on_input clears
              +-- recipient input_required clears
```

This is similar to an A2A task reaching `input-required`, but it is not the same
object. A2A has `Task.status.state`; postman has per-message reply obligations
plus node status projection.

## 4. Term Mapping

| A2A term                     | Postman term or surface                                               | Current recommendation                                                                                                                  |
| ---------------------------- | --------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `contextId`                  | Stored frontmatter `params.contextId`; status JSON `context_id`       | Prefer A2A-natural `contextId` in future generated detail views; expose stored keys unchanged only as archive truth.                    |
| `taskId` / `task_id`         | External issue, plan, mkmd task, or future daemon-owned task          | Do not generate daemon `task_id` until postman owns tasks.                                                                              |
| `Message.messageId`          | Stored frontmatter `messageId`; public JSON `message_id`; file name   | Prefer an A2A-aware `message.messageId` projection in future detail JSON; keep raw stored keys only where they report archive metadata. |
| `Message.role`               | `from`, `to`, pane title, node name                                   | Keep local peer routing names; A2A user/agent roles do not fit.                                                                         |
| `Message.parts`              | Markdown body; possible future body projection from #396              | Treat body as one `text/markdown` part only if #396 adds parts.                                                                         |
| `Message.metadata`           | YAML frontmatter fields under `params` and other top-level sections   | Project structured frontmatter in `pop` JSON after #396.                                                                                |
| `referenceTaskIds`           | Optional future `reference_task_ids` for external task IDs            | Do not overload `input_request_set_id`; they are different concepts.                                                                    |
| `Artifact`                   | mkmd task artifacts or produced files                                 | Use as analogy; do not claim A2A Artifact without IDs/parts.                                                                            |
| `Task.status.state`          | `visible_state`, `severity`, `nodes[*].flow`                          | Provide an alignment view, not direct replacement.                                                                                      |
| `input-required`             | Recipient `input_required`; open `input_request_id`                   | Good conceptual match for required recipient input.                                                                                     |
| `TaskStatus.message`         | Reply-required message body and footer guidance                       | Use as analogy when explaining why a reply is needed.                                                                                   |
| `AgentCard`                  | `postman.md`, `postman.toml`, `nodes/*`, `skills/*/SKILL.md`          | Consider an AgentCard-like export later; not implemented now.                                                                           |
| `AgentSkill`                 | Published postman skills and node role capabilities                   | Keep skill docs precise and installable.                                                                                                |
| A2A protocol binding         | None                                                                  | Do not claim JSON-RPC, gRPC, or HTTP+JSON binding support.                                                                              |

## 5. State Alignment

A2A v1.0 task states are task lifecycle states. Postman `visible_state` values
are operator states for tmux nodes. They can be mapped for explanation, but
should not be stored as direct replacements.

| Postman state or severity              | Closest A2A term                 | Recommendation                                                                 |
| -------------------------------------- | -------------------------------- | ------------------------------------------------------------------------------ |
| `initial` visible state                | `unknown` / unspecified          | Keep postman-specific; no positive live evidence has arrived yet.              |
| `ready`                                | No active task, or `completed`   | Keep `ready`; A2A `completed` only applies to an explicit task.                |
| `working` severity                     | `working`                        | Good alignment when pane activity or queued delivery proves work in progress.  |
| `waiting` visible state                | Client waiting for a task update | Keep `waiting`; A2A has no sender-side wait state.                             |
| `pending` visible state                | `input-required`                 | Best alignment; recipient has input/reply work to do.                          |
| `input_required` input request         | `input-required`                 | Strong match; consider an `input_request_id` alias only after #396 design.     |
| `waiting_on_input` input request       | No direct TaskState              | Keep; it is sender-side projection, not task lifecycle.                        |
| `blocked` severity                     | `input-required` or `failed`     | Do not auto-map; blocked may be recoverable or terminal depending on evidence. |
| `delivery_stuck` severity              | No direct TaskState              | Keep postman-specific; it is transport status.                                 |
| `delivery_failure` severity            | No direct TaskState              | Keep postman-specific; dead letters are routing failures, not task failures.   |
| `stale` visible state                  | `unknown` / unspecified          | Keep postman-specific; previously known pane or session evidence is stale.     |
| `unavailable` session fallback         | `unknown` / unspecified          | Keep postman-specific; it is a daemon ownership/canonical-status condition.    |
| Future explicit cancellation           | `canceled`                       | Use only when a task/request is explicitly canceled.                           |
| Future explicit rejection              | `rejected`                       | Use only when a node explicitly refuses the work.                              |
| Future authentication wait             | `auth-required`                  | Use only when an auth handoff is modeled as a first-class obligation.          |

Recommended public wording:

- `visible_state` stays postman-native: `initial`, `ready`, `waiting`,
  `pending`, `stale`, with session `unavailable`, because it reports node
  observability, not an A2A task lifecycle.
- `severity` stays postman-native: `ok`, `working`, `expected_wait`,
  `needs_action`, `blocked`, `attention_stale`, `delivery_stuck`,
  `delivery_failure`, because it reports operator urgency and transport
  status.
- Future A2A alignment fields can be additive, for example
  `a2a_alignment.task_state_hint: "input-required"`.

## 6. Detailed `get-status` JSON Criteria

Detailed `get-status` JSON should be understandable to A2A-aware users with
low conceptual friction. It is the right surface for clear semantic names and
explanatory hierarchy. Compact and one-line status views should stay compact,
but detailed JSON does not need terse internal abbreviations when a clearer
A2A-natural shape is accurate.

Design rules:

- Prefer A2A-natural terminology and hierarchy where the local concept
  really matches A2A. For example, recipient-side required input can be
  explained with `input-required` alignment, while sender-side
  `waiting_on_input` needs a postman-specific explanation.
- Document postman-specific differences and their reasons directly in detailed
  JSON docs. Delivery status, tmux pane liveness, and input-request closure are
  local runtime facts, not A2A protocol objects.
- Avoid false equivalence. Do not label `get-status` as `GetTask`, do not call
  node visibility a `TaskState`, and do not imply an A2A task exists before the
  daemon owns a task lifecycle.
- Prefer explicit semantic keys over terse internal abbreviations in detailed
  JSON. A longer key is acceptable when it reduces ambiguity for A2A-aware
  operators.
- Keep compact and one-line views compact. Those surfaces may keep terse
  status tokens as long as the detailed JSON exposes the clearer explanation.
- Do not retain an awkward shape only because an older output used it. Cleaner
  A2A-natural detailed shapes should win unless a concrete safety reason, such
  as exact input-request closure or archive truth, is named.

## 7. ID and Field Alignment

Current postman input-request fields are operationally accurate:

| Current field                  | Meaning                                                           | A2A alignment note                                     |
| ------------------------------ | ----------------------------------------------------------------- | ------------------------------------------------------ |
| `input_request_id`             | Exact required-input request opened for a recipient               | Similar to one `input-required` request, not A2A wire. |
| `fills_input_request_id`       | Exact input request that this reply resolves                      | Mechanical closure field; good for projection.         |
| `input_request_set_id`         | Reserved aggregate for grouped input requests                     | Not the same as A2A `referenceTaskIds`.                |
| `reply_to`                     | Message ID this message references                                | Similar to linking message history, not task identity. |
| `input_required`               | Recipient-side open reply-required work                           | Best local name for `input-required` behavior.         |
| `waiting_on_input`             | Sender-side open wait for required reply                          | Postman-specific perspective.                          |
| `inspect-input --id`           | Finds open input requests by `input_request_id` or message ID     | Useful operator API.                                   |
| `nodes[*].flow.input_requests` | Status JSON projection of open required-input requests            | Correct home for detailed input-request state.         |
| `pop` JSON fields              | Claimed message envelope path and structured frontmatter metadata | Expose archive truth without embedding full body.      |

Counterpart names if the project later models richer request closure:

| Candidate                         | Verdict       | Reason                                                                    |
| --------------------------------- | ------------- | ------------------------------------------------------------------------- |
| `fills_input_request_id`          | Current       | Short, mirrors exact closure, and says the request is satisfied.          |
| `responds_to_input_request_id`    | Rejected      | A response may not actually satisfy or close the request.                 |
| `satisfies_input_request_id`      | Plausible     | Precise, but long and implies semantic validation beyond current closure. |
| `input_response_to`               | Rejected      | Less consistent with existing `reply_to` and `fills_*` naming.            |
| `input_request_id` only           | Insufficient  | Needs a counterpart field for exact closure.                              |

Forward path:

1. Keep `input_request_id` and `fills_input_request_id` authoritative for exact
   closure.
2. In detailed JSON, use A2A-natural projections only where the projected
   concept is accurate.
3. Prefer `input_request_id` plus `fills_input_request_id` for the
   required-input projection.
4. Do not add aliases solely to preserve older output shapes. Add them when
   they make the generated detailed shape clearer without obscuring closure
   semantics.

## 8. Frontmatter Metadata

The favored minimal A2A respect marker is:

```yaml
protocol:
  respects:
    a2a_protocol: "1.0"
```

Use `"1.0"` for machine-comparable alignment because A2A service versioning
uses major/minor strings such as `1.0`. The docs source section can still
record the exact released source as `1.0.0`.

Example message frontmatter:

```yaml
---
params:
  contextId: "20260506-001721-c1d9"
  from: "orchestrator"
  to: "worker"
  messageId: "20260506-120000-sfb93-example.md"
  replyPolicy: "required"
  input_request_id: "ireq_example"
protocol:
  respects:
    a2a_protocol: "1.0"
---

Please investigate the failing check.
```

This marker says the archive was written with A2A v1.0 terminology in mind. It
does not say the message is an A2A `Message` object or that the daemon supports
an A2A binding.

Avoid adding `postman_schema_version` as part of A2A respect. Schema versioning
is a separate product need. Add it only if the archive or JSON output needs a
local schema/version contract that is independent of the A2A reference.

## 9. Where to Store A2A Reference Metadata

| Surface                       | Recommendation                                  | Why                                                                      |
| ----------------------------- | ----------------------------------------------- | ------------------------------------------------------------------------ |
| Public docs                   | Required                                        | Explains what version the project is using for terminology.              |
| mkmd task artifacts           | Useful for design work                          | Records research evidence and decisions without changing wire data.      |
| Build metadata                | Optional                                        | Useful for `version` output, but does not make archives self-describing. |
| `postman.toml` / `postman.md` | Plausible default source                        | Lets a session declare alignment policy once.                            |
| Message frontmatter           | Recommended only if #396 accepts metadata noise | Makes each archived message self-describing and visible in pop JSON.     |
| `get-status` JSON             | Optional additive field                         | Useful for operators, but status is runtime state, not archive truth.    |
| CLI help text                 | Mention sparingly                               | Help should not become a protocol essay.                                 |

If the reference is written into every message, use the minimal shape above and
keep it non-normative. Do not write `a2a_compliant: true`.

## 10. Per-Message Metadata Benefits and Risks

| Benefit                         | Detail                                                               |
| ------------------------------- | -------------------------------------------------------------------- |
| Self-describing archives        | A read message can be understood without knowing the current binary. |
| Better `pop` JSON after #396    | Structured frontmatter can expose the A2A reference directly.        |
| Mixed-version history support   | Old and new messages can carry different alignment references.       |
| Per-message migration evidence  | Migration tools can decide what field vocabulary was intended.       |
| Debuggable generated templates  | Footer/template regressions become visible in message files.         |

| Risk                              | Detail                                                                  |
| --------------------------------- | ----------------------------------------------------------------------- |
| Metadata noise                    | Every stored message gains lines that do not affect routing.            |
| Misleading wording                | `a2a_protocol` could be misread as compliance unless docs are explicit. |
| Version churn                     | A2A patch releases could cause unnecessary template updates.            |
| Template update blast radius      | Message template changes touch tests, docs, skills, and examples.       |
| Archive inconsistency             | Some historical messages will not have the marker.                      |
| Pop payload growth                | #396 wants machine-friendly JSON; extra metadata should stay compact.   |

Recommendation: add the marker to message frontmatter only when #396 changes
`pop` JSON to expose structured frontmatter and `markdown_path`. Until then,
the docs page and task artifacts are enough.

## 11. #396 Pop JSON and Frontmatter Implications

Issue #396 proposes that `pop` stays JSON by default, stops embedding the full
Markdown body, returns a stable `markdown_path`, and exposes all structured YAML
frontmatter needed by agents.

That direction fits A2A respect well. A2A `Message.parts` can become a future
body projection, while frontmatter remains metadata.

Path and body-reference display follows
[Path Display Policy](path-display-policy.md): user-facing home paths can be
`~`-shortened, while absolute file paths need an explicit machine-readable
field when agents must open the file directly. `pop` remains the receiver-owned
state transition: daemon notifications hint that mail exists, and the receiver
calls `pop` to claim the inbox item and obtain the archived body reference.

Example future `pop` JSON shape:

```json
{
  "status": "message",
  "message_id": "20260506-120000-sfb93-example.md",
  "from": "orchestrator",
  "to": "worker",
  "reply_policy": "required",
  "input_request_id": "ireq_example",
  "markdown_path": "~/.local/state/tmux-a2a-postman/read/worker/20260506-120000-sfb93-example.md",
  "markdown_absolute_path": "/absolute/path/to/read/worker/20260506-120000-sfb93-example.md",
  "params": {
    "contextId": "20260506-001721-c1d9",
    "messageId": "20260506-120000-sfb93-example.md"
  },
  "protocol": {
    "respects": {
      "a2a_protocol": "1.0"
    }
  },
  "remaining": 0
}
```

Example optional future body projection:

```json
{
  "message_id": "20260506-120000-sfb93-example.md",
  "parts": [
    {
      "text": "Please investigate the failing check.",
      "media_type": "text/markdown"
    }
  ],
  "metadata": {
    "input_request_id": "ireq_example"
  }
}
```

The default #396 JSON should not include full `body` or `content`; it should
always link to `markdown_path` for human reading and `markdown_absolute_path`
when a machine-readable absolute path is needed. If the project adds `parts`,
make it an explicit opt-in or a compact body-only mode so the default remains
machine-friendly.

No separate user-facing alias is introduced for now. The command name `pop`
continues to carry the mailbox state-machine meaning, while pane notifications
and help text describe the user-visible action as claiming/opening the message
and obtaining its archived body path/reference.

## 12. AgentCard Alignment

A2A AgentCard describes an agent's identity, interfaces, capabilities, security
requirements, input/output modes, and skills. Postman has related material, but
spread across local files and installed skills:

| A2A AgentCard field        | Postman source                                          |
| -------------------------- | ------------------------------------------------------- |
| `name` / `description`     | Node role names and role text in `postman.md`           |
| `supportedInterfaces`      | None today; possible future custom binding metadata     |
| `version`                  | Binary `version` output and release tag                 |
| `documentationUrl`         | Repository docs and README                              |
| `capabilities`             | Daemon commands, status surfaces, input-request support |
| `defaultInputModes`        | Markdown messages via `send-heredoc`                    |
| `defaultOutputModes`       | Markdown messages and JSON status/pop output            |
| `skills`                   | `skills/*/SKILL.md`                                     |
| `securitySchemes`          | None today; local tmux/user trust boundary              |
| `securityRequirements`     | None today                                              |

Recommendation: do not generate AgentCard JSON yet. First decide whether
postman wants a real custom binding or only a documentation analogy. If a
future export exists, call it AgentCard-like until it exposes the required A2A
fields honestly.

## 13. Version and Migration Policy

Recommended baseline:

```yaml
protocol:
  respects:
    a2a_protocol: "1.0"
```

Use `protocol.respects.a2a_protocol` for archive metadata, future generated
frontmatter, or explicit internal alignment metadata. A2A version is a
reference marker, not a runtime behavior knob. Use longer names only when the
surface needs to be clearer:

- `a2a_protocol_reference`: good for docs or prose when avoiding compliance
  implications.
- `a2a_alignment_version`: good for build or config metadata if it means
  postman's own mapping document version.
- `protocol.respects.a2a_protocol`: best minimal shape for YAML frontmatter.

Rejected baseline:

```yaml
postman_schema_version: "1"
```

That may be useful later, but it solves local schema migration, not A2A
respect.

Archive and migration handling:

1. Treat missing `protocol.respects.a2a_protocol` as no declaration, not as
   weaker compliance.
2. Do not rewrite historical archives automatically.
3. Let `pop` JSON expose the marker when present.
4. Preserve exact stored frontmatter keys when reporting raw archive metadata.
   For newly generated detailed JSON, choose the cleaner A2A-natural shape
   unless a concrete safety reason is named.
5. Document the current A2A source version in docs even if per-message
   frontmatter stays absent.
6. Treat historical TOML A2A-version entries as inert input, not as an active
   configuration contract or design constraint.

## 14. Concrete Recommendations

1. Add this docs page as the canonical A2A respect explanation.
2. Do not preserve older output shapes as the reason for A2A alignment choices.
   Cleaner A2A-natural detailed shapes win unless a concrete safety or
   archive-truth reason is named.
3. Keep postman-native runtime fields authoritative where they name concrete
   local mechanics:
   `input_request_id`, `fills_input_request_id`, `input_required`,
   `waiting_on_input`, `visible_state`, and `severity`.
4. Use `protocol.respects.a2a_protocol: "1.0"` as the favored future
   frontmatter/config marker.
5. Defer per-message metadata until #396 removes default body/content JSON and
   exposes structured frontmatter plus `markdown_path`.
6. Do not introduce `postman_schema_version` as part of this A2A alignment.
7. Do not expose A2A versioning as user-configurable TOML; keep A2A versioning
   in docs or future generated metadata.
8. If adding A2A-aligned detailed JSON fields, prefer `input_request_id` and
   `fills_input_request_id` for an accurate input-request projection. Keep exact
   input-request fields only on surfaces that close or audit input requests.
9. Keep `task_id` external until the daemon owns task lifecycle creation,
   state transitions, and terminal states.
10. Treat `input_request_set_id` as grouped input-request aggregation, not
    `referenceTaskIds`.

## 15. Rejected Alternatives

| Alternative                            | Reason rejected                                                          |
| -------------------------------------- | ------------------------------------------------------------------------ |
| Claim an A2A server surface            | Postman does not implement A2A discovery, operations, or bindings.       |
| Replace `visible_state` with TaskState | Node status and task lifecycle are different models.                     |
| Add `postman_schema_version` now       | A2A reference metadata does not require local archive schema versioning. |
| Add a TOML A2A version knob            | It looks user-configurable but changes no runtime behavior.              |
| Put A2A reference only in build output | Archived messages would not be self-describing.                          |
| Put full A2A AgentCard in docs now     | Would imply a discovery surface that does not exist.                     |

## 16. Open Questions

| Question                                      | Decision needed                                                             |
| --------------------------------------------- | --------------------------------------------------------------------------- |
| Per-message marker                            | Should every new message carry `protocol.respects.a2a_protocol`?            |
| Version string                                | Should archives store `"1.0"` or exact release `"1.0.0"`?                   |
| Alias timing                                  | Should #396 add `input_request_id` aliases or only expose current fields?   |
| Task ownership                                | Should postman ever generate task IDs, or leave tasks to mkmd/issues?       |
| AgentCard-like export                         | Should config/skills become a generated discovery document later?           |
| Body projection                               | Should #396 expose `parts`, or only `markdown_path` and frontmatter?        |
| Custom binding                                | Is a file/tmux A2A custom binding a goal, or is terminology respect enough? |

These choices need user judgment because they affect public archives, JSON
contracts, and how strongly the project appears to align with A2A.
