# Config SSOT

`internal/config/postman.default.toml` is the SSOT for user-configurable
defaults.

## Policy

- `DefaultConfig()` initializes structural and derived containers only:
  `Edges`, `Nodes`, `NodeOrder`, `PingSkillCatalogs`, and
  `CompactionSkillCatalogs`.
- Non-zero defaults for public config fields belong in
  `internal/config/postman.default.toml`.
- `postman.toml` is optional. With no user TOML, embedded defaults are enough
  to run the daemon.
- A minimal `postman.md` may contain only a Mermaid `edges` section. Nodes
  referenced by those edges are materialized with empty `NodeConfig` values.
- The human-facing startup PING target should normally be marked in Mermaid
  with the `ui_node` class, keeping topology-facing settings in one diagram.
- `postman.md` frontmatter may set `skill_path` to generate an agent skill
  catalog from selected `SKILL.md` frontmatter without inlining skill bodies.
  Entries with omitted `inject` are appended to normal role context and remain
  runtime-agnostic.
- `postman.md` frontmatter `skill_path` entries with `inject: ping` generate
  catalogs for every daemon PING. Entries with `inject: compaction_ping`
  generate catalogs only for compaction-triggered daemon PINGs. Both stay out
  of normal role context. Runtime selectors for these catalogs live in
  `postman.md`; exact runtime support is currently Claude Code and Codex CLI,
  and an omitted `runtime` is shared plus fallback.
- PING catalog paths, including runtime-specific entries, must be
  global/user-level: `~/...` or absolute. Repo-local relative paths remain
  supported only for normal role catalogs and are invalid for PING catalogs.
- Rendered skill catalogs dedupe by frontmatter `name`. Later path entries
  override earlier entries with the same rendered name. Runtime-specific
  compaction PING catalogs evaluate shared entries first and the matching
  runtime entries second, so runtime entries override shared entries without
  injecting duplicate skill bodies.
- Omitted `skills` means all skills under that path. A present `skills` value
  should be a YAML list of explicit skill directory names; `skills: [all]`
  selects a real skill named `all`. The scalar `skills: all` remains accepted
  as a legacy shorthand for existing configs.
- Runtime IDs, product names, and conventional skill-directory metadata are
  centralized in `internal/agentruntime`.
- XDG/global config and explicit `--config` files merge on top of embedded
  defaults. Implicit project-local `.tmux-a2a-postman/` overlays are not part
  of the runtime config surface.
- Non-configurable implementation timings must be named constants in code, not
  inline literals or hidden public config fields.

## Why

Operators should not need a large generated TOML file just to run postman. A
minimal setup can keep topology in Markdown and inherit all behavior from the
embedded default TOML.

Keeping defaults in one file also makes reviews easier: changing a public
default means changing `postman.default.toml`, docs, and tests together.

Claude Code and Codex CLI runtime differences are tracked separately in
[Agent Runtime Feature Differences](../agent-runtime-feature-differences.md).
Do not encode runtime-specific behavior in `postman.toml` defaults unless a
follow-up issue explicitly changes the public config surface.

## Regression Guards

- `internal/config/config_test.go` asserts `DefaultConfig()` stays limited to
  structural containers.
- `internal/config/config_test.go` asserts each non-zero embedded default loaded
  into public TOML-tagged fields is declared in `postman.default.toml`.
- Config tests assert CWD-local `.tmux-a2a-postman/` files do not override XDG
  or explicit config.

## Minimal Topology

````markdown
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

This creates `messenger`, `orchestrator`, `worker`, and `critic` nodes even
when no `[messenger]`, `[orchestrator]`, `[worker]`, or `[critic]` TOML sections
exist. The `ui_node` class marks `messenger` as the startup auto-PING target.
