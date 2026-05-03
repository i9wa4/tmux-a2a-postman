---
name: postman-config-auditor
license: MIT
description: |
  Audits tmux-a2a-postman configuration, topology, postman.md syntax, and node templates.
  Use when:
  - Reviewing or fixing postman.toml, postman.md, or nodes/* configuration
  - Checking whether Mermaid edges, node sections, or role templates match implementation behavior
  - Diagnosing dead-letter, missing route, quiet node, unread backlog, or late reply behavior
  - Adding or renaming nodes and needing a config consistency review
  Do not use as a generic CLI reference; use tmux-a2a-postman help for command syntax.
---

# postman-config-auditor

Audit tmux-a2a-postman configuration with implementation-level accuracy. This
skill covers topology, `postman.md` syntax, merge order, and node role
templates. For command syntax, prefer `tmux-a2a-postman help`.

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
- Project-local templates cannot enable shell expansion for themselves.
- Nodes referenced by valid `edges` are materialized automatically, even when no
  node template is defined.
- A `postman.toml` file is optional. Treat a TOML file that only restates
  embedded defaults as deletion-worthy unless it documents an intentional local
  override.

## 3. Audit Checklist

### 3.1. Topology

- Confirm every intended route appears as a bidirectional `---` edge.
- Confirm Mermaid `postman.md` edges use `---`, not arrows such as `-->`.
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
- Confirm frontmatter only uses the supported one-line `key: value` subset.

### 3.3. Node Role Templates

- Confirm each active node has clear reply behavior, completion words, and
  escalation rules when the workflow needs a response.
- Confirm intentionally quiet nodes say that explicitly.
- Confirm templates use `tmux-a2a-postman send`, `pop`, and `get-health`
  instead of raw runtime filesystem manipulation.
- Confirm templates do not duplicate context injected by system templates:
  `message_footer`, `draft_template`, `daemon_message_template`,
  `notification_template`, or dead-letter notification text.
- Distinguish postman node names from generic prose. For example, a repo may
  use a `critic` node while still describing generated subagents as reviewers.

### 3.4. Runtime Symptoms

- Use `tmux-a2a-postman get-health` for structured state and
  `tmux-a2a-postman get-health-oneline` for compact coordination.
- Treat `pending` as unread inbox mail.
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
