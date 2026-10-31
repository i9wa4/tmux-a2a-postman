# Configuration source of truth

## 1. Current configuration

The active commands read one bounded absolute `postman.md`. Default selection
is `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md`, or the corresponding
`HOME/.config` path. `--config` and `--postman-md` are alternative explicit
selectors; relative or simultaneous selectors fail. Every invocation rereads
configuration. No TOML overlays, daemon defaults or runtime restart is involved.

Mermaid `---` edges define local peers; `command_approver_node` selects a
reviewer and `interface_node` selects an interface role. YAML `workspace_tree`
declares direct parent/child interface routes. Native tmux evidence must still
verify both endpoints. See the canonical
[format reference](../../skills/postman-config-auditor/references/postman-md.md).

## 2. Agent runtime differences

[Agent Runtime Feature Differences](../agent-runtime-feature-differences.md)
records which behavior belongs to Postman and which belongs to the agent.
Recipient setup and an executable handler supply activation; role prose and
skills alone cannot wake an idle process. Native sandbox policies stay with
the agent runtime.
