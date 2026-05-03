# tmux-a2a-postman

[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/i9wa4/tmux-a2a-postman)

tmux agent-to-agent message delivery daemon.

It runs one daemon per Unix user, treats tmux pane titles as role/node names,
and delivers `send` messages to filesystem-backed inboxes. Any AI coding agent
that runs in tmux can take any role; agents read mail with `pop` and inspect
shared health with `get-health` or `get-health-oneline`.

## 1. Concept

```mermaid
---
title: tmux-a2a-postman architecture
---
graph TD
    human["human operator\nstarts one daemon"]
    config["postman.md / postman.toml\nroles, edges, templates"]
    daemon["postman daemon\nroutes by edges"]
    mailbox["filesystem mailboxes\npost/ inbox/{node}/ read/ dead-letter/"]
    health["status views\nTUI / get-health / get-health-oneline"]

    subgraph tmux["tmux session: any AI coding agent can take any role"]
        messenger["messenger\nClaude Code"]
        orchestrator["orchestrator\nCodex CLI"]
        worker["worker\nClaude Code"]
        critic["critic\nany AI coding agent"]
    end

    human --> daemon
    config --> daemon
    tmux -->|send / reply| daemon
    daemon -->|deliver / notify| tmux
    daemon <--> mailbox
    mailbox -->|pop reads mail| tmux
    daemon --> health
```

## 2. Prerequisites

- tmux >= 3.0

## 3. Installation

```sh
go install github.com/i9wa4/tmux-a2a-postman@latest
```

Or with Nix:

```sh
nix run github:i9wa4/tmux-a2a-postman
```

### 3.1. (Optional) Agent Skills

The postman binary works without the `skills/` directory. These AI assistant
skills help agents discover the first command and audit configuration:

- `postman-send-message`: minimal entry point for sending the first postman
  message.
- `postman-config-auditor`: audits `postman.md`, `postman.toml`, `nodes/*`,
  topology, and node templates.

These skills are published through GitHub Releases; no separate skill registry
is required. Install GitHub CLI 2.90.0 or newer first; see the
[GitHub CLI installation guide](https://github.com/cli/cli#installation). Then
install all bundled skills for your agent.

For Claude Code:

```sh
gh skill install i9wa4/tmux-a2a-postman postman-send-message --agent claude-code --scope user
gh skill install i9wa4/tmux-a2a-postman postman-config-auditor --agent claude-code --scope user
```

For Codex:

```sh
gh skill install i9wa4/tmux-a2a-postman postman-send-message --agent codex --scope user
gh skill install i9wa4/tmux-a2a-postman postman-config-auditor --agent codex --scope user
```

See the
[GitHub CLI `gh skill install` manual](https://cli.github.com/manual/gh_skill_install)
for supported agents and scopes.

## 4. Usage

The human operator starts one daemon for their Unix user:

```sh
tmux-a2a-postman start
```

Agents then run commands from their own tmux panes. The pane title identifies
the sending role/node, independent of whether the pane is Claude Code, Codex, or
another AI coding agent:

```sh
tmux-a2a-postman send --to worker --body "implement X"
```

The daemon discovers panes by title and routes messages through
filesystem-backed inboxes. A recipient agent usually runs `pop` after the pane
notification or message footer tells it mail is waiting.

Use explicit subcommands; bare `tmux-a2a-postman` prints usage and does not
start the daemon. The exact CLI reference is built into the binary:

```sh
tmux-a2a-postman help
tmux-a2a-postman help commands
tmux-a2a-postman help config
tmux-a2a-postman help directories
```

`get-health`, `get-health-oneline`, and the default TUI are views over the same
canonical contract. Agents should prefer `get-health` for structured session
JSON and `get-health-oneline` for compact coordination.

## 5. Configuration

`postman.toml` is optional; embedded defaults from
`internal/config/postman.default.toml` are enough to run the daemon. A minimal
`postman.md` can contain only Mermaid edges:

```mermaid
---
title: postman.md edge topology
---
graph LR
    messenger --- orchestrator
    orchestrator --- worker
    orchestrator --- worker-alt
    orchestrator --- critic
    orchestrator --- boss
    guardian --- critic
    orchestrator --- agent
```

````markdown
## `edges`

```mermaid
graph LR
    messenger --- orchestrator
    orchestrator --- worker
    orchestrator --- worker-alt
    orchestrator --- critic
    orchestrator --- boss
    guardian --- critic
    orchestrator --- agent
```
````

Every node referenced by `edges` is materialized automatically. Define node
templates only when you need role-specific instructions:

````markdown
## `worker`

### `role`

Primary task executor.

### Workflow

Execute tasks from orchestrator. Report DONE or BLOCKED.
````

Place config files under `$XDG_CONFIG_HOME/tmux-a2a-postman/`, or under
project-local `.tmux-a2a-postman/` for overrides. Detailed `postman.md` syntax
lives in
[skills/postman-config-auditor/references/postman-md.md](skills/postman-config-auditor/references/postman-md.md).
Configuration defaults and merge policy live in
[docs/design/config-ssot.md](docs/design/config-ssot.md), and daemon ownership
details live in
[docs/design/daemon-session-model.md](docs/design/daemon-session-model.md).
