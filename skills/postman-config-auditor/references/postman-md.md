# postman.md Format Reference

This reference describes the Markdown format parsed by tmux-a2a-postman. It is
based on the implementation in `internal/config/markdown.go` and the load order
in `internal/config/config.go`.

## 1. Purpose

`postman.md` is a Markdown overlay for topology, shared templates, node role
text, and a small set of YAML frontmatter settings. It is not a general
Markdown configuration language.

Supported files:

| File type            | XDG path                                            | Project-local path                  |
| -------------------- | --------------------------------------------------- | ----------------------------------- |
| Main Markdown config | `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md`      | `.tmux-a2a-postman/postman.md`      |
| Split node Markdown  | `$XDG_CONFIG_HOME/tmux-a2a-postman/nodes/{node}.md` | `.tmux-a2a-postman/nodes/{node}.md` |

## 2. Global Frontmatter

Only a leading `---` block is parsed. The main `postman.md` frontmatter is
YAML. Keep it small: scalar settings plus the `skill_path` list are the
supported public surface.

Supported global keys in `postman.md`:

| Key             | Effect                                                       |
| --------------- | ------------------------------------------------------------ |
| `ui_node`       | Sets `Config.UINode` as a frontmatter override               |
| `reply_command` | Sets `Config.ReplyCommand` when non-empty                    |
| `skill_path`    | Appends generated skill catalogs to `Config.CommonTemplate`  |

Rules:

- Prefer marking the UI node in the Mermaid graph with `class <node> ui_node`.
  Inline `:::ui_node` also works. Frontmatter `ui_node` is still supported as an
  explicit override.
- Empty frontmatter `ui_node:` is meaningful and explicitly clears `ui_node`.
- `skill_path` may be a scalar path or a YAML list of path entries.
- A `skill_path` list item may be a scalar path or a mapping with `path` and
  `skills`.
- `skills` may be `all` or a YAML list of explicit skill directory names.
- Omitted `skills` means `all`.
- Glob patterns such as `postman-*` are unsupported; list skill names
  explicitly.
- An unclosed frontmatter block is an error.

Example:

```text
---
reply_command: tmux-a2a-postman send-heredoc --to {from_node}
skill_path:
  - path: skills
    skills:
      - repo-local
      - bash
      - github
      - markdown
  - path: ~/.claude/skills
    skills:
      - postman-config-auditor
      - postman-session-operator
---
```

Each `path` points to a directory containing one subdirectory per skill, each
with a `SKILL.md` file. Relative paths are resolved from the directory
containing the `postman.md` file, `~/...` expands to the current user's home
directory, and symlinked skill directories are followed. The generated catalog
reads `name` and `description` from selected `SKILL.md` frontmatter and appends
a compact Markdown list to `common_template`. Skill frontmatter may use
single-line `description`, `description: |`, or `description: >-`.

## 3. H2 Section Parsing

The main `postman.md` parser only recognizes h2 headings that contain a
backtick-wrapped name.

Parsed examples:

```text
## `edges`
## `worker`
## 1. `worker-alt` Node
```

Ignored examples:

```text
# `worker`
### `worker`
## Worker
## edges
```

Reserved h2 names:

| H2 name           | Meaning                        |
| ----------------- | ------------------------------ |
| `edges`           | Mermaid topology section       |
| `common_template` | Sets `Config.CommonTemplate`   |
| `message_footer`  | Sets or appends message footer |

All other h2 backtick names become node sections. The section body runs until
the next parsed h2 heading or end of file.

## 4. Edges Section

The `edges` h2 section must contain a fenced `mermaid` block. The parser reads
the first Mermaid fence in that section.

````text
## `edges`

```mermaid
graph LR
    messenger --- orchestrator
    orchestrator --- worker
    orchestrator --- critic
    class messenger ui_node
    classDef ui_node fill:#e0f2fe
```
````

Edge rules:

- Only `---` is parsed as an edge operator.
- The UI node may be marked with a `class messenger ui_node` statement, or with
  inline class syntax such as `messenger:::ui_node`.
- `graph`, `flowchart`, `subgraph`, `end`, `direction`, `classDef`, `class`,
  `style`, `click`, `linkStyle`, `accTitle`, and `accDescr` statements are
  skipped.
- `%%` Mermaid comments are stripped.
- Multiple statements on one line can be separated by `;`.
- Mermaid node decorations such as labels, shapes, classes, and quoted names
  are normalized to the node id.
- Arrows such as `-->` are not valid postman edges.
- Node ids are configuration-owned protocol names. The parser does not know
  that `critic`, `reviewer`, or other role-like words are synonyms.

Equivalent normalized edge output:

```text
messenger --- orchestrator
orchestrator --- worker
orchestrator --- critic
```

## 5. Node Sections

A node section is any non-reserved h2 backtick heading in main `postman.md`.
The node name is the first backtick-wrapped value, lowercased.

```text
## `worker`

### `role`

Primary task executor.

### Workflow

Execute tasks from orchestrator. Reply with DONE or BLOCKED.
```

Node role extraction:

- An h3 `role` section is preferred.
- The role body runs until the next h2 or h3 heading.
- The h3 `role` section is removed from the node template body.
- If no h3 `role` section exists, node-section frontmatter key `role` is used.
- After role extraction, leading frontmatter is stripped from the template.

Other h3 sections are kept in the node template.

Frontmatter fallback example:

```text
## `critic`

---
role: Reviewer
---

Review changes and reply with APPROVED or REJECTED.
```

## 6. Split Node Markdown

`nodes/{node}.md` defines one node. The node name comes from the filename
without `.md`.

The split-node parser supports the same role extraction as node sections:

- Prefer an h3 `role` section.
- Fall back to leading frontmatter key `role`.
- Strip role section and frontmatter from the stored template.

Split node Markdown does not parse `edges`, `common_template`, or
`message_footer`.

## 7. Merge Behavior

Effective configuration is loaded from low to high priority:

1. Embedded defaults from `internal/config/postman.default.toml`
2. XDG `postman.toml`
3. XDG `nodes/*.toml`
4. XDG `nodes/*.md`
5. XDG `postman.md`
6. Project-local `postman.toml`
7. Project-local `nodes/*.toml`
8. Project-local `nodes/*.md`
9. Project-local `postman.md`

Important rules:

- Main config files merge node fields rather than replacing whole nodes.
- Split `nodes/*.toml` files replace that node at their load layer.
- Split `nodes/*.md` files update only non-empty role and template fields.
- `postman.md` edges replace lower-layer edges only when the parsed edge list is
  non-empty.
- A Mermaid `ui_node` class in the `edges` graph sets `ui_node` when
  frontmatter does not set it. Frontmatter `ui_node` wins within the same
  Markdown file.
- XDG `postman.md` `message_footer` replaces the lower-layer footer.
- Project-local `postman.md` `message_footer` appends to the effective base
  footer.
- `skill_path` is applied within the Markdown layer that declares it; selected
  generated catalogs are appended to that layer's `common_template` content.
- Nodes referenced by valid edges are materialized automatically.

## 8. Minimal Valid postman.md

````text
## `edges`

```mermaid
graph LR
    messenger --- orchestrator
    orchestrator --- worker
```
````

This is enough to define the topology. Node templates are optional.
