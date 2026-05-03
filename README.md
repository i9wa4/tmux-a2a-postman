# tmux-a2a-postman

[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/i9wa4/tmux-a2a-postman)

tmux agent-to-agent message delivery daemon.

Any AI coding agent can occupy the roles you define, turning a tmux session into
a durable workspace for human-directed handoffs, delegation, and review.

It runs one daemon per local user account, treats tmux pane titles as role/node
names, and delivers `send` messages to filesystem-backed inboxes. Agents read
mail with `pop` and inspect shared health with `get-health` or
`get-health-oneline`.

## 1. Concept

```mermaid
---
title: tmux-a2a-postman architecture
---
graph TD
    operator((human\noperator))
    config["postman.md / postman.toml\nroles, edges, templates\nui_node = messenger"]
    daemon["postman daemon\nroutes mail\nsends auto PING"]
    mailbox[("filesystem mailboxes\npost/ inbox/{node}/ read/ dead-letter/")]

    subgraph session_main["tmux session: main workspace"]
        messenger["messenger\nhuman-facing ui_node"]
        orchestrator["orchestrator"]
        worker["worker"]
    end

    subgraph session_review["tmux session: review workspace"]
        reviewer["reviewer"]
        worker_alt["worker-alt"]
    end

    operator --> |starts / configures| daemon
    operator <--> |talks with| messenger
    config --> daemon
    messenger <--> |brief / status| orchestrator
    orchestrator <--> |delegate / report| worker
    orchestrator <--> |delegate / report| worker_alt
    orchestrator <--> |review request| reviewer
    daemon <--> mailbox
    daemon -.->|delivers mail + auto PING| session_main
    daemon -.->|delivers mail + auto PING| session_review
    mailbox -.->|stores mail for pop| session_main
    mailbox -.->|stores mail for pop| session_review

    classDef operatorType fill:#fff7ed,stroke:#c2410c,color:#111827
    classDef configType fill:#eef2ff,stroke:#4f46e5,color:#111827
    classDef daemonType fill:#e0f2fe,stroke:#0369a1,color:#0f172a
    classDef storageType fill:#ecfdf5,stroke:#047857,color:#0f172a
    classDef agentType fill:#f8fafc,stroke:#475569,color:#0f172a

    class operator operatorType
    class config configType
    class daemon daemonType
    class mailbox storageType
    class messenger,orchestrator,worker,reviewer,worker_alt agentType
    style session_main fill:#ffffff,stroke:#94a3b8,color:#0f172a
    style session_review fill:#ffffff,stroke:#94a3b8,color:#0f172a
```

## 2. Prerequisites

- macOS or Linux
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

For Codex CLI:

```sh
gh skill install i9wa4/tmux-a2a-postman postman-send-message --agent codex --scope user
gh skill install i9wa4/tmux-a2a-postman postman-config-auditor --agent codex --scope user
```

See the
[GitHub CLI `gh skill install` manual](https://cli.github.com/manual/gh_skill_install)
for supported agents and scopes.

## 4. Usage

The human operator starts one daemon for their local user account:

```sh
tmux-a2a-postman start
```

After `start`, each discovered node receives an auto PING. If a node pane is
opened or restarted later, it receives the same PING when discovered. A PING is
normal inbox mail: the recipient sees the pane notification, runs `pop`, and
reads its role plus reply guidance.

Agents then run commands from their own tmux panes. The pane title identifies
the sending role/node, independent of whether the pane is Claude Code, Codex
CLI, or another AI coding agent:

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
    orchestrator --- reviewer
    orchestrator --- boss
    guardian --- reviewer
    orchestrator --- agent
```

````markdown
## `edges`

```mermaid
graph LR
    messenger --- orchestrator
    orchestrator --- worker
    orchestrator --- worker-alt
    orchestrator --- reviewer
    orchestrator --- boss
    guardian --- reviewer
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
