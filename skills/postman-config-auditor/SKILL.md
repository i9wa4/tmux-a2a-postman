---
name: postman-config-auditor
license: MIT
description: |
  Audits tmux-a2a-postman configuration, topology, postman.md syntax, node
  templates, and postman.md versus SKILL.md responsibility boundaries.
  Use when:
  - Reviewing or fixing postman.toml, postman.md, or nodes/* configuration
  - Checking whether Mermaid edges, node sections, or role templates match implementation behavior
  - Deciding whether instructions belong in postman.md, a generated skill
    catalog, or a SKILL.md body
  - Checking whether a skill description is specific enough to trigger when
    postman.md delegates instructions to that skill
  - Diagnosing dead-letter, missing route, quiet node, unread backlog, or late reply behavior
  - Adding or renaming nodes and needing a config consistency review
  Do not use as a generic CLI reference; use tmux-a2a-postman help for command syntax.
---

# postman-config-auditor

Audit tmux-a2a-postman configuration with implementation-level accuracy. This
skill covers topology, `postman.md` syntax, merge order, node role templates,
and the boundary between always-injected postman instructions and on-demand
skill instructions. For command syntax, prefer `tmux-a2a-postman help`.

When `postman.md` syntax or merge behavior matters, read
`references/postman-md.md` before judging the file.

## 1. Scope

Audit these files when present:

| Purpose                        | XDG path                                                   | Project-local path                    |
| ------------------------------ | ---------------------------------------------------------- | ------------------------------------- |
| Main TOML config               | `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml`           | `.tmux-a2a-postman/postman.toml`      |
| Main Markdown config           | `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md`             | `.tmux-a2a-postman/postman.md`        |
| Split node TOML                | `$XDG_CONFIG_HOME/tmux-a2a-postman/nodes/{node}.toml`      | `.tmux-a2a-postman/nodes/{node}.toml` |
| Split node Markdown            | `$XDG_CONFIG_HOME/tmux-a2a-postman/nodes/{node}.md`        | `.tmux-a2a-postman/nodes/{node}.md`   |
| Embedded defaults and comments | `internal/config/postman.default.toml`                     | same repository file                  |

`$XDG_CONFIG_HOME` defaults to `~/.config` when unset.

## 2. Config Model

Check the effective configuration in this order:

1. Embedded defaults from `internal/config/postman.default.toml`
2. XDG `postman.toml`
3. XDG `nodes/*.toml`
4. XDG `nodes/*.md`
5. XDG `postman.md`
6. Project-local `postman.toml`
7. Project-local `nodes/*.toml`
8. Project-local `nodes/*.md`
9. Project-local `postman.md`

Important merge rules:

- Non-empty scalar values override lower layers.
- `edges` replaces lower-layer edges only when the override has at least one
  edge.
- Node configs merge field by field for main config files.
- Split `nodes/*.toml` files replace that node at their layer.
- Project-local `postman.md` appends `message_footer` to the effective base
  footer.
- `postman.md` frontmatter `skill_path` generates compact skill catalogs from
  selected `SKILL.md` files and appends them to that Markdown layer's
  `common_template`. `skill_path` accepts YAML list entries with `path` and
  `skills`; `skills` is either `all` or an explicit YAML list.
- Project-local templates cannot enable shell expansion for themselves.
- Nodes referenced by valid `edges` are materialized automatically, even when no
  node template is defined.
- A `postman.toml` file is optional. Treat a TOML file that only restates
  embedded defaults as deletion-worthy unless it documents an intentional local
  override.
- Public non-zero defaults are owned by
  `internal/config/postman.default.toml` and guarded by config SSOT tests. Do
  not treat `DefaultConfig()` literals as product defaults.

## 3. Audit Checklist

### 3.1. Topology

- Confirm every intended route appears as a bidirectional `---` edge.
- Confirm Mermaid `postman.md` edges use `---`, not arrows such as `-->`.
- Confirm the human-facing node is marked in the Mermaid graph with
  `class <node> ui_node` or `:::ui_node`, unless frontmatter intentionally
  overrides or clears `ui_node`.
- Confirm missing routes explain dead-letter behavior before blaming role
  templates.
- Confirm node names in templates are reachable from the sender when the text
  instructs an agent to contact that node.
- Treat node names as local protocol identifiers, not generic job titles. Do
  not rename nodes such as `critic` to `reviewer` unless the graph, pane
  titles, and user intent all require that rename.

### 3.2. postman.md Syntax

- Use `references/postman-md.md` as the detailed syntax contract.
- Confirm parsed sections use h2 headings with backtick names, such as
  `edges` and `worker`.
- Confirm the `edges` section contains a fenced `mermaid` block.
- Confirm role text lives under an h3 `role` section or in supported
  frontmatter.
- Confirm global `postman.md` frontmatter stays within the supported YAML
  surface: scalar settings plus `skill_path` path entries.
- Prefer keeping `ui_node` in the Mermaid `edges` graph. Treat frontmatter
  `ui_node` as an explicit override, not the normal topology declaration.
- If `skill_path` is set, confirm relative paths resolve from the declaring
  `postman.md` directory, `~/...` points to the current user's home directory,
  and each selected skill name maps to a subdirectory containing `SKILL.md`.
- Confirm `skills` uses `all` or explicit YAML list items. Glob patterns such
  as `postman-*` are unsupported.
- Confirm generated skill catalogs match `SKILL.md` frontmatter `name` and
  `description`, rather than hand-maintained stale lists.

### 3.3. Node Role Templates

- Confirm each active node has clear reply behavior, completion words, and
  escalation rules when the workflow needs a response.
- Confirm intentionally quiet nodes say that explicitly.
- Confirm templates use `tmux-a2a-postman send-heredoc`, `pop`, and
  `get-health` instead of raw runtime filesystem manipulation.
- Confirm templates do not duplicate context injected by system templates:
  `message_footer`, `draft_template`, `daemon_message_template`,
  `notification_template`, or dead-letter notification text.
- Confirm templates do not inline full skill bodies when `skill_path` can
  generate a skill catalog and the full instructions can remain in `SKILL.md`.
- Confirm any instruction moved from `postman.md` into a skill is named in that
  skill's frontmatter `description` with concrete trigger conditions. The
  generated catalog only exposes metadata, so hidden body text is not enough.
- Distinguish postman node names from generic prose. For example, a repo may
  use a `critic` node while still describing generated subagents as reviewers.

### 3.4. postman.md / SKILL.md Balance

Use this rubric when a config is too large, too vague, or duplicated across
`postman.md` and skill files.

For `tmux-a2a-postman pop` size, optimize the payload that is actually
delivered:

```text
common_template + generated skill catalog + target node template + system footer
```

Total `postman.md` line count is less important than these injected parts.
Measure section sizes before editing, then reduce in this order:

1. `common_template`, because every node receives it.
2. Generated skill catalog descriptions, because `skill_path` appends them to
   `common_template`.
3. The specific node template that receives noisy `pop` output.
4. Other node templates, only when they are noisy for their own recipients.

Keep content in `postman.md` when it is needed before an agent can safely
choose a skill:

- topology, node names, and routing expectations
- reply-required versus no-reply behavior, completion words, and escalation
  rules
- state-machine semantics that affect `get-health` or `get-health-oneline`
- role-specific authority boundaries, such as who may approve or implement
- compact reminders that prevent prompt deadlocks or broken message flow
- the `skill_path` declaration and a short rule to read listed `SKILL.md` files
  before execution

Move content to `SKILL.md` when it is reusable procedure rather than routing
contract:

- tool-specific workflows, command recipes, and examples
- repo or domain conventions that are only needed for matching tasks
- long checklists, style guides, debugging loops, and review rubrics
- engine-specific usage details unless they affect message delivery
- content that can be selected from the generated skill catalog

Move tmux-a2a-postman product-spec explanations out of local `postman.md` when
they can be selected by skill:

| Product-spec content                                  | Preferred skill            |
| ----------------------------------------------------- | -------------------------- |
| `pop`, `send-heredoc`, `get-health`, reply semantics  | `postman-session-operator` |
| `pending`, `waiting`, `stale`, queues                 | `postman-session-operator` |
| dead-letter diagnosis and safe retry flow             | `postman-session-operator` |
| `postman.md` syntax, edges, merge order               | `postman-config-auditor`   |
| `skill_path` catalog behavior                         | `postman-config-auditor`   |

Flag these imbalance patterns:

- hand-maintained skill tables that duplicate the generated `skill_path`
  catalog
- `postman.md` sections that inline full skill bodies or long examples
- role templates that repeat the same procedural checklist across nodes
- stray h2 headings without backtick names after a parsed node section; the
  parser does not treat them as new node sections, so they can leak into the
  previous node template
- detailed explanations in `common_template` where a one-line contract plus a
  `SKILL.md` or docs reference would preserve behavior
- skills that redefine postman routing, topology, or state-machine behavior
  instead of referring back to `postman.md`
- skills whose body contains important moved rules but whose `description`
  does not mention the user/task situations that should trigger them
- ambiguous instructions where agents cannot tell whether a rule is a
  transport contract, role contract, or task-specific procedure

When recommending a balance fix, classify each moved or retained block as one
of:

| Class               | Destination        | Reason                                               |
| ------------------- | ------------------ | ---------------------------------------------------- |
| Transport contract  | `postman.md`       | Needed for delivery, replies, status, or escalation  |
| Role contract       | `postman.md`       | Needed to behave correctly as this node              |
| Skill index         | `skill_path`       | Generated from skill frontmatter, not hand-written   |
| Task procedure      | `SKILL.md`         | Needed only after a relevant task is selected        |
| Reference material  | `references/*.md`  | Too detailed for the skill body unless needed        |
| Runtime default     | embedded TOML      | Product default, not a local role instruction        |

### 3.5. Runtime Symptoms

- Use `tmux-a2a-postman get-health` for structured state and
  `tmux-a2a-postman get-health-oneline` for compact coordination.
- Treat `pending` as inbound reply-required action.
- Treat `waiting` as outbound reply-required mail waiting for a response.
- Treat `stale` as missing, unavailable, or unknown pane/session state before
  changing templates. A live pane that is merely quiet should not be diagnosed
  as stale.
- Treat dead-letter as a routing/config issue until edges prove otherwise.

## 4. Findings Format

Return findings first, ordered by severity:

```text
[SEVERITY] Node: {node-or-global}
File: {path}
Check: {check name}
Result: FAIL
Issue: {description}
Fix:
  {exact replacement text or concrete edit}
```

Severity values:

- `BLOCKING`: breaks routing, parsing, or core message flow.
- `IMPORTANT`: causes likely agent confusion or repeated workflow failure.
- `MINOR`: drift, duplication, or maintainability issue.

When the user asks you to fix the repository, edit the source files directly.
When the user only asks for an audit, report findings and recommended patches.

## 5. Nix Store Warning

Before patching deployed config, check whether it is a read-only Nix store
symlink:

```sh
ls -la "$XDG_CONFIG_HOME/tmux-a2a-postman"
```

If files resolve into the Nix store, patch the editable source, usually in
dotfiles, or create a project-local `.tmux-a2a-postman/` override. Report the
deployment constraint as a finding, not as a skill failure.
