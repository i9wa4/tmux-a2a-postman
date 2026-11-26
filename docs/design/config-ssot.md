# Configuration source of truth

## 1. Current configuration

Configuration-reading commands read one bounded absolute `postman.md`. Default
selection is `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md`, or the
corresponding `HOME/.config` path. `--config` is the explicit selector; relative
or simultaneous selectors fail. Each configuration-reading child invocation
rereads the selected file. The resident foreground `daemon` does not read it. No
TOML overlays or runtime restart is involved. An explicit `heartbeat` reads
`daemon.defaults.heartbeat.body`; an optional foreground `daemon -- heartbeat`
asks the current CLI for `daemon.defaults.heartbeat.every` on each scheduling
cycle. A custom file supplies its own values without inheriting embedded
defaults. Intervals below one second are invalid. These values do not change the
routing registration fingerprint.

Mermaid `---` edges define local peers; `command_approver_node` selects a
reviewer and `interface_node` selects an interface role. Nearest live interface
panes in containing canonical workspace directories define direct routes.
Native tmux evidence must still verify both endpoints. See the canonical
[format reference](../../help/postman-md.md).

## 2. Agent runtime differences

[Agent Runtime Feature Differences](agent-runtime-feature-differences.md)
records which behavior belongs to Postman and which belongs to the agent.
Recipient setup and an executable handler supply activation; role prose and
skills alone cannot wake an idle process. Native sandbox policies stay with
the agent runtime.
