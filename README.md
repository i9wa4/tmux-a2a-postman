# tmux-a2a-postman

Turn terminal AI consoles into a configurable team, with durable Markdown mail
and no resident Postman service.

```mermaid
flowchart TB
    human[You: objective and criteria] --> entry[Human-facing agent]
    subgraph team[Your existing tmux consoles]
        entry -->|delegate| builder[Build]
        entry -->|request review| reviewer[Review]
        builder -->|results and evidence| entry
        reviewer -->|findings| entry
    end
    entry -->|reconciled result| human
    builder -.-> mail[(Readable Markdown mail)]
    reviewer -.-> mail
```

Keep your clients, models and tool permissions. Postman supplies explicit
routes, durable delivery, input notifications and correlated replies. Delivery
does not prove task completion; agents reconcile results against your objective.

## 1. Configure the organization

```mermaid
flowchart TB
    config[postman.md] --> policy[Routes + role policy]
    policy --> manager[Manager / interface]
    manager --- worker[Builder]
    manager --- review[Reviewer]
    worker --- approval[Approver]
    manager <-->|direct route| child[Child interface]
    child --> childworker[Child worker]
```

Use the embedded standard team without a configuration file, or customize
the whole organization in one file. Artifact review and command approval are
different roles; review coordinates decisions and is not an OS sandbox.
Direct parent/child routes require declared interfaces and distinct workspaces.
The configuration reference defines the supported fields.

## 2. Small commands, durable files

```mermaid
flowchart TB
    config[postman.md: routing and policy] --> cli[Finite Postman CLI]
    consoles[Existing Codex / Claude / local consoles] -->|tools| cli
    cli -->|commit and claim| files[(Mail, receipts and captures)]
    cli -->|text hint and Enter| consoles
    files --> cache[Saved server snapshot]
    cache --> getters[Read-only JSON / status line]
    external[Optional external adapter] <-->|provisioned file channel| cli
```

Transport uses native tmux panes and ordinary files, without model APIs.
Optional [`launch-agents`](help/launching.md) creates one declared team and
exits, without startup prompt injection or supervision. External services own
their adapter and activation; no native GitHub/mobile service is implied.

```mermaid
---
config:
  sequence:
    actorMargin: 20
    width: 95
---
sequenceDiagram
    participant A as Sender
    participant P as Postman
    participant B as Recipient
    A->>P: send-heredoc<br/>--to role
    P->>B: Durable inbox<br/>+ input hint
    B->>P: pop
    P-->>B: Archive path
    Note over B: Read whole file<br/>through EOF
    B->>P: send-heredoc<br/>--to claimed-id
    P-->>A: Exact reply<br/>+ input hint
```

Messages carry the recipient's instructions, contacts and exact reply command.
Notification uncertainty leaves committed mail intact. Logs remain until you
delete them.

## 3. Install and try

Requires tmux and an agent console able to run shell tools. These docs describe
this checkout; older release tags may expose a different CLI. Build the same
source you are reading:

```sh
git clone https://github.com/i9wa4/tmux-a2a-postman.git
cd tmux-a2a-postman
nix build
./result/bin/tmux-a2a-postman version
```

Without Nix, use the Go version selected in `go.mod` and `go install .`.
Put the resulting binary on your consoles' `PATH`. Client installation,
authentication and sandbox settings remain yours.

The binary includes complete Markdown guidance: run
`tmux-a2a-postman help setup` or `tmux-a2a-postman help <topic>` without a
checkout, tmux or configuration. Thin Skills discover installed workflow
references; `tmux-a2a-postman help skills` explains the thin entries and
consumer-owned setup. Updating the binary updates the detailed procedures.

Install a thin Skill with GitHub CLI:

```sh
gh skill install i9wa4/tmux-a2a-postman postman-usage --agent codex --scope user
```

Use `--agent claude-code` for Claude Code. Replace `postman-usage` with any
name from `tmux-a2a-postman skills` to install another entry. Each Skill
reads its detailed references with `tmux-a2a-postman skills <name> <ref>`.

Start with the [builtin standard team](config/postman.md) and
`tmux-a2a-postman skills postman-orchestrate`; no dotfiles or Skill installation
is required. That same default `postman.md` contains all startup presets:

| Preset             | Consoles and layout                                                   |
| ------------------ | --------------------------------------------------------------------- |
| `codex` / `claude` | Eight roles using the selected plain client                           |
| `preset-p`         | Existing vde-layout Codex team: two columns, seven roles              |
| `preset-w`         | Existing vde-layout mixed Codex/Claude team: two columns, seven roles |

The two vde-layout presets retain their original model, effort and permission
arguments. Preview the exact commands before choosing a preset:

```sh
tmux-a2a-postman launch-agents --list
tmux-a2a-postman launch-agents preset-p --dry-run
```

Remove `--dry-run` to launch. Existing consoles can instead start with a send.
Inspect the builtin `postman.md` with
`tmux-a2a-postman skills postman-config-auditor default`.
Read `help config` to export a custom copy and select it.

Follow the [first exchange](skills/postman-usage/references/quickstart.md) and
the [configuration reference](help/postman-md.md) to choose roles and routes.

The repository mirrors the installed interface:

```text
help/                 CLI usage: help <topic>
config/               bundled configuration assets
skills/<name>/
  SKILL.md            short entry: skills <name>
  references/         workflows: skills <name> <ref>
  scripts/            optional helpers
  evals/              offline validation
docs/                design, research, development and validation
testdata/            test-only fixtures
```

## 4. Guides and references

- [Quickstart and operations](skills/postman-usage/references/quickstart.md):
  setup, mail, observations and troubleshooting.
- [Configuration reference](help/postman-md.md):
  anatomy, examples and exact supported fields.
- [CLI contract](help/cli-spec.md) and `tmux-a2a-postman help`: command inputs
  and outputs.
- [Command review](help/execution.md),
  [server status](help/server-status.md), and
  [external file integration](help/external-filesystem-channel.md).
- [Installable Skills](skills/): usage, sending, recall, configuration audit and
  team management, planning, records, orchestration and review.
- [Operating procedures](skills/postman-orchestrate/references/intake.md),
  [planning](skills/postman-plan/references/investigation.md),
  [records](skills/postman-records/references/selection.md), and
  [review](skills/postman-review/references/routing.md): the canonical Skill
  references available in the binary.
- [Migration inventory](docs/design/standard-team-migration.md): source
  ownership, portability and verification boundaries.
- [Contributing](docs/development/contributing.md): development, deterministic
  tmux tests and evidence limits.

This repository's [AGENTS.md](AGENTS.md) guides contributors. Your runtime
`postman.md` configures your team; it is a separate instruction surface.
