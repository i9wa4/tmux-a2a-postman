# Agent Runtime Feature Differences

This document is the canonical long-term record for Claude Code and Codex CLI
differences that affect tmux-a2a-postman operation. Temporary task artifacts may
record discoveries while work is in progress, but durable decisions belong here
before an issue is closed.

Scope is documentation and operating policy only. Runtime behavior changes need
a separate issue.

## Status Vocabulary

| Status                 | Meaning                                                                 |
| ---------------------- | ----------------------------------------------------------------------- |
| Aligned                | The repo expects the same operator behavior from both runtimes.          |
| Intentional divergence | The runtimes differ and the repo deliberately keeps separate handling.   |
| Temporary gap          | The repo wants parity, but implementation or documentation is incomplete. |
| Unsupported            | The repo does not plan to support that runtime behavior.                 |
| Monitor                | The behavior is owned by the external runtime and must be rechecked.     |

## Comparison Table

| Feature / behavior area              | Claude Code behavior                                                                                                   | Codex CLI behavior                                                                                                      | Parity status          | Source / reference                                                                                                                                                                       | Owner / update trigger                                                                                       | Last reviewed date |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- | ---------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ | ------------------ |
| Postman node identity                | Uses the tmux pane title as the sender or recipient role; Claude-specific identity is not part of the postman protocol. | Uses the tmux pane title as the sender or recipient role; Codex-specific identity is not part of the postman protocol.  | Aligned                | [Configuration](../README.md#configuration)                                                                                                                                              | Runtime maintainer when pane discovery, node naming, or routing changes.                                     | 2026-05-06         |
| Message transport commands           | Runs the same `send-heredoc`, `pop`, `get-status`, `get-status-oneline`, `inspect-input`, and `inspect-message` CLI.   | Runs the same `send-heredoc`, `pop`, `get-status`, `get-status-oneline`, `inspect-input`, and `inspect-message` CLI.    | Aligned                | [Quick Start](../README.md#quick-start), [Messaging Rules](../README.md#messaging-rules), [commands help](../internal/cli/helptext/commands.txt)                                         | CLI maintainer when public commands, flags, or output contracts change.                                      | 2026-05-06         |
| Skill distribution                   | Bundled skills are installed with `gh skill install ... --agent claude-code`.                                           | Bundled skills are installed with `gh skill install ... --agent codex`.                                                 | Aligned                | [Agent Skills](../README.md#agent-skills), [Releasing](../RELEASING.md)                                                                                                                  | Release maintainer when skill names, frontmatter, GitHub CLI behavior, or release flow changes.              | 2026-05-06         |
| Skill discovery in `postman.md`      | `skill_path` exposes selected `SKILL.md` frontmatter in generated role context; entries with `inject: ping` can add `claude` runtime catalogs only to compaction-triggered PINGs. Shared entries without `runtime` are included in runtime-specific catalogs and fallback. | `skill_path` exposes selected `SKILL.md` frontmatter in generated role context; entries with `inject: ping` can add `codex` runtime catalogs only to compaction-triggered PINGs. Shared entries without `runtime` are included in runtime-specific catalogs and fallback. | Monitor                | [postman.md reference](../skills/postman-config-auditor/references/postman-md.md#2-global-frontmatter)                                                                                    | Harness maintainer when skill trigger semantics or catalog behavior changes.                                 | 2026-05-07         |
| Runtime settings and managed config  | Claude Code has its own settings hierarchy for permissions, hooks, MCP, subagents, and related runtime behavior.        | Codex CLI has its own configuration, approval mode, and sandbox model outside the postman config surface.                | Intentional divergence | [Claude Code settings](https://docs.claude.com/en/docs/claude-code/settings), [OpenAI Codex CLI getting started](https://help.openai.com/en/articles/11096431-openai-codex-ligetting-started) | Harness maintainer when managed settings, approval modes, sandbox modes, or MCP behavior change.             | 2026-05-06         |
| Hooks and permission denial          | Claude Code supports configured hook events and permission rules in its runtime.                                        | Codex CLI approval and sandbox behavior is runtime-owned; postman treats denial text as ordinary pane/operator evidence. | Intentional divergence | [Claude Code hooks](https://docs.claude.com/en/docs/claude-code/hooks), [OpenAI Codex CLI getting started](https://help.openai.com/en/articles/11096431-openai-codex-ligetting-started)      | Harness maintainer when hook events, denial behavior, or approval handling changes.                          | 2026-05-06         |
| Sandbox and network constraints      | Access is controlled by Claude Code permissions, settings, and any configured hooks.                                    | Codex CLI modes may restrict writes, command execution, sandbox scope, and network access depending on mode.            | Intentional divergence | [Claude Code settings](https://docs.claude.com/en/docs/claude-code/settings), [OpenAI Codex CLI getting started](https://help.openai.com/en/articles/11096431-openai-codex-ligetting-started) | Harness maintainer when local safety policy or runtime mode names change.                                    | 2026-05-06         |
| Native subagents and delegation      | Claude Code has native subagents; postman still treats durable tmux panes as the cross-agent transport.                 | Codex-native delegation is runtime-owned; postman still treats durable tmux panes as the cross-agent transport.         | Intentional divergence | [Claude Code subagents](https://code.claude.com/docs/en/sub-agents), [Configuration](../README.md#configuration)                                                                          | Harness maintainer when workflows depend on native subagents instead of tmux pane roles.                     | 2026-05-06         |
| Context-compaction recovery signals  | Pane capture scans for Claude context-compaction markers when deciding recovery PING behavior.                          | Pane capture scans for Codex context-compaction markers when deciding recovery PING behavior.                           | Aligned                | [Configuration](../README.md#configuration), [configuration design](design/config-ssot.md)                                                                                                | Runtime maintainer when pane capture markers, scan depth, or recovery PING behavior changes.                 | 2026-05-06         |
| Release and changelog review cadence | Claude Code runtime changes must be reviewed from Claude Code release notes or installed-version documentation.         | Codex CLI runtime changes must be reviewed from OpenAI Codex CLI docs, help center notes, or installed-version output.  | Monitor                | [Claude Code changelog](https://code.claude.com/docs/en/changelog), [OpenAI Codex CLI collection](https://help.openai.com/en/collections/13193998-codex-cli), [Releasing](../RELEASING.md)   | Release or harness maintainer before changing skills, hooks, managed settings, or runtime-specific guidance. | 2026-05-06         |
| Temporary discoveries and decisions  | Task artifacts may capture Claude-specific findings during active work, then this table records durable differences.    | Task artifacts may capture Codex-specific findings during active work, then this table records durable differences.     | Aligned                | This document                                                                                                                                                                           | Issue owner before closing a task that discovers a runtime difference.                                       | 2026-05-06         |
| Public GitHub path hygiene           | Public docs, issues, PRs, comments, and commit messages must use repo-relative paths or stable URLs only.               | Public docs, issues, PRs, comments, and commit messages must use repo-relative paths or stable URLs only.               | Aligned                | [AGENTS](../AGENTS.md), [CLAUDE](../CLAUDE.md)                                                                                                                                          | Every contributor before posting public GitHub text or committing docs.                                      | 2026-05-06         |

`skill_path` is the primary catalog surface. Entries with omitted `inject` or
`inject: context` are always-injected, runtime-agnostic compact catalogs.
Entries with `inject: ping` are the compaction-only surface for larger or
runtime-selected catalogs, which keeps full catalogs out of ordinary turns.
Ping-injected paths, including runtime-specific catalogs, must be
global/user-level paths (`~/...` or absolute); repo-relative paths remain
available only for non-ping context catalogs. Duplicate rendered skill names
are deduped: later entries win, and runtime-specific entries override shared
ping entries.
`compaction_skill_path` remains accepted as a compatibility form.

## Update Workflow

1. When a task artifact, review, release note, or local debugging session finds
   a Claude Code versus Codex CLI difference, add or update a row in the
   comparison table before closing the task.
2. Prefer shared postman behavior when the difference can be expressed through
   `postman.md`, `postman.toml`, common templates, shared skills, or CLI docs.
3. Mark a difference as `Intentional divergence` when the product surfaces are
   meaningfully different and shared behavior would hide an important runtime
   constraint.
4. Mark a difference as `Temporary gap` only when there is an intended follow-up
   parity change. Link the issue or create one before closing the discovery
   task.
5. Mark a difference as `Unsupported` when the repo deliberately avoids that
   runtime behavior. Include the reason in the row text or a nearby note.
6. For `Monitor` rows, recheck the runtime source when updating Claude Code,
   Codex CLI, bundled skills, hook policy, sandbox policy, or release docs.
7. Update the `Last reviewed` date for every row whose source was rechecked,
   even if the behavior did not change.

## Verification

Before publishing documentation or GitHub text about runtime differences:

- Confirm new references are repo-relative paths or stable web URLs.
- Confirm no public doc, issue, PR, comment, or commit message contains
  machine-local checkout paths, Nix store paths, or user-home shorthand.
- Confirm `README.md`, relevant `docs/` pages, and runtime references link back
  to this document instead of creating a second long-term comparison table.
- Confirm runtime behavior changes are not bundled into documentation-only
  updates.
