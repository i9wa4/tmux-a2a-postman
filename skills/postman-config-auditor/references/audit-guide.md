# Config Audit Guide

Audit these files when present:

| Purpose                        | Path                                                       |
| ------------------------------ | ---------------------------------------------------------- |
| Main TOML config               | `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml`           |
| Main Markdown config           | `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md`             |
| Split node TOML                | `$XDG_CONFIG_HOME/tmux-a2a-postman/nodes/{node}.toml`      |
| Split node Markdown            | `$XDG_CONFIG_HOME/tmux-a2a-postman/nodes/{node}.md`        |
| Embedded defaults and comments | `internal/config/postman.default.toml`                     |

`$XDG_CONFIG_HOME` defaults to `~/.config` when unset.

## Config Model

Check the effective configuration in this order:

1. Embedded defaults from `internal/config/postman.default.toml`
2. XDG `postman.toml`
3. XDG `nodes/*.toml`
4. XDG `nodes/*.md`
5. XDG `postman.md`

Implicit project-local `.tmux-a2a-postman/` overlays are retired. For
workspace-specific checks, use an explicit `--config` path or edit the
deployed XDG config source.

Important merge rules:

- Non-empty scalar values override lower layers.
- `edges` replaces lower-layer edges only when the override has at least one
  edge.
- Node configs merge field by field for main config files.
- Split `nodes/*.toml` files replace that node at their layer.
- `postman.md` frontmatter `skill_path` generates compact skill catalogs from
  selected `SKILL.md` files and appends them to that Markdown layer's
  `common_template` unless a mapping uses `inject: ping` or
  `inject: compaction_ping`.
- `skill_path` accepts YAML list entries with `path`, optional `inject`, and
  optional `skills`. Omitted `skills` means every skill under that path; a
  present `skills` list selects explicit skill directory names, including a real
  skill named `all`. The scalar `skills: all` remains accepted as legacy input.
- `inject: ping` generates catalogs for every daemon PING. `inject:
  compaction_ping` generates catalogs only for compaction-triggered daemon
  PINGs. Both stay out of `common_template`.
- PING entries must use `~/...` or absolute paths. Repo-local relative paths
  remain valid only for normal role catalogs.
- Rendered catalogs are unique by skill frontmatter `name`; later path entries
  override earlier entries with the same rendered name.
- Nodes referenced by valid `edges` are materialized automatically, even when no
  node template is defined.
- A `postman.toml` file is optional. Treat a TOML file that only restates
  embedded defaults as deletion-worthy unless it documents an intentional local
  override.
- Public non-zero defaults are owned by
  `internal/config/postman.default.toml` and guarded by config SSOT tests.

## Topology

- Confirm every intended route appears as a bidirectional `---` edge.
- Confirm Mermaid `postman.md` edges use `---`, not arrows such as `-->`.
- Confirm the human-facing node is marked in the Mermaid graph with
  `class <node> ui_node` or `:::ui_node`, unless frontmatter intentionally
  overrides or clears `ui_node`.
- Confirm missing routes explain dead-letter behavior before blaming role
  templates.
- Confirm node names in templates are reachable from the sender when the text
  instructs an agent to contact that node.
- Treat node names as local protocol identifiers, not generic job titles.

## postman.md Syntax

- Use the format reference as the detailed syntax contract.
- Confirm parsed sections use h2 headings with backtick names, such as `edges`
  and `worker`.
- Confirm the `edges` section contains a fenced `mermaid` block.
- Confirm role text lives under an h3 `role` section or in supported
  frontmatter.
- Confirm global frontmatter stays within the supported surface: scalar
  settings plus `skill_path` path entries.
- Prefer keeping `ui_node` in the Mermaid `edges` graph. Treat frontmatter
  `ui_node` as an explicit override.
- For normal role catalogs, confirm relative paths resolve from the declaring
  `postman.md` directory, `~/...` points to the current user's home directory,
  and each selected skill name maps to a subdirectory containing `SKILL.md`.
- For `inject: ping` or `inject: compaction_ping`, confirm the intent and
  require `~/...` or absolute paths.
- Confirm generated skill catalogs match `SKILL.md` frontmatter `name` and
  `description`, rather than hand-maintained stale lists.

## Node Role Templates

- Confirm each active node has clear reply behavior, completion words, and
  escalation rules when the workflow needs a response.
- Confirm intentionally quiet nodes say that explicitly.
- Confirm templates use `tmux-a2a-postman send-heredoc`, `pop`, and
  `get-status` instead of raw runtime filesystem manipulation.
- Confirm templates do not duplicate context injected by system templates:
  `message_footer`, `draft_template`, `daemon_message_template`,
  `notification_template`, or dead-letter notification text.
- Confirm templates do not inline full skill bodies when `skill_path` can
  generate a role or compaction PING catalog and the full instructions can
  remain in `SKILL.md`.
- Confirm instructions moved into a skill are named in that skill's frontmatter
  `description` with concrete trigger conditions.

## postman.md / SKILL.md Balance

Measure the payload that is actually delivered:

```text
common_template + generated skill catalog + target node template + system footer
common_template + target node template + compaction catalog + daemon PING body
```

Reduce noisy payloads in this order:

1. `common_template`
2. Generated `skill_path` catalog descriptions
3. The specific node template receiving noisy `pop` output
4. `inject: ping` or `inject: compaction_ping` entries only when daemon PINGs
   become too large
5. Other node templates only when they are noisy for their own recipients

Keep topology, reply rules, escalation rules, state-machine semantics, authority
boundaries, `skill_path` declarations, and short pre-execution skill-reading
rules in `postman.md`.

Move reusable workflows, command recipes, style guides, debugging loops, review
rubrics, and long examples to `SKILL.md` or `references/*.md`.

## Runtime Symptoms

- Use `tmux-a2a-postman get-status` for structured state and
  `tmux-a2a-postman get-status-oneline` for compact coordination.
- Treat `initial` as neutral: no positive live evidence yet. Compact status
  uses `⚫` for `initial` and unavailable session fallbacks.
- Treat `pending` as inbound reply-required action.
- Treat `waiting` as outbound reply-required mail waiting for a response.
- Treat `stale` as previously known but stale pane/session state before
  changing templates.
- Treat dead-letter as a routing/config issue until edges prove otherwise.

## Findings Format

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

## Nix Store Warning

Before patching deployed config, check whether it is a read-only Nix store
symlink:

```sh
ls -la "$XDG_CONFIG_HOME/tmux-a2a-postman"
```

If files resolve into the Nix store, patch the editable source, usually in
dotfiles, or pass an explicit `--config` path for a temporary run. Do not rely
on implicit project-local `.tmux-a2a-postman/` overrides. Report the deployment
constraint as a finding, not as a skill failure.
