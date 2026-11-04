# launch-agents — expand one explicit agent-team preset, then exit

```sh
tmux-a2a-postman launch-agents <preset> [--config /absolute/postman.md]
  [--socket /absolute/tmux.sock] [--cwd /absolute/team-directory]
  [--session dedicated-name] [--dry-run]
tmux-a2a-postman launch-agents --list [--config /absolute/postman.md]
```

YAML presets contain ordered columns. Agent panes have existing role/command
pairs; a title-only pane has a unique non-role title and no command, and starts
an ordinary interactive shell. It is not a mail recipient. All presets are
validated before tmux mutation. --list and --dry-run are inert: no commands,
tmux creation, state registration or persistent logs. Commands are explicit
user shell source, not words to split or message policy.

Launch selects the invoking TMUX server unless --socket is supplied. A new
socket requires an existing parent directory. From a verified tmux pane, the
default target is a new window in that pane's session. Outside tmux, the
default is a dedicated preset/workspace-derived session. Explicit --session or
--socket creates a dedicated session. An existing dedicated target or live
workspace owner is rejected without modifying it. --cwd defaults to the
invocation directory and must resolve below HOME. Columns are left-to-right;
panes top-to-bottom.

Only the new window or session and its panes are created. Native titles, cwd and
config continuity are established before role commands. The first pane is
focused. The existing server's default-shell (or SHELL, then /bin/sh for a new
server) loads interactive/login initialization. A title-only pane runs that
shell with no declared command. First-prompt-only hooks are not promised;
use an explicit environment-loader command when needed. No private state of an
already running shell is copied. No model credentials or bypass flags are added.

With a custom file, new consoles inherit TMUX_A2A_POSTMAN_CONFIG as its absolute
routing/policy selection. With the builtin team, it is explicitly empty and
XDG_CONFIG_HOME retains the selected automatic location, overriding stale server
values without creating a default file. Operational CLI precedence is explicit
--config, this inherited
selection, then the normal XDG/HOME default. Commands which clear inherited
environment must preserve it or pass --config. Both status getters remain
cache-only and ignore configuration selection. No generated config is needed.

There is no initial role message, PING, synthetic mail, supervisor or respawn
loop. Successful exit leaves the team alive; created processes are not proof of
authentication or readiness. Ordinary manually/external-launched agents work.

One nonblocking workspace setup lock and scoped ownership receipts prevent
concurrent/repeated starts. A prior/uncertain started target is never replayed;
inspect its outcome and deliberately choose a new target for a new launch.
Partial failure cleans only positively owned new resources. Commands already
started may have irreversible effects; stopping panes does not undo them.
A killed launcher may leave resources/uncertain receipts: no force-reset,
force-unlock or automatic repair is provided.

With an absent automatic user configuration, the standard `dev` preset provides
an `editor` shell on the left and the `agent` role with its original explicit
Codex command on the right. The original VDE `current-window` setting maps to
a new window in the invoking session, or a dedicated session outside tmux; no
existing window is modified. The same default `postman.md` contains
vde-layout's `preset-p` and `preset-w`: two
columns of four and three roles, preserving their exact client, model, effort
and permission arguments. Use `--dry-run` to inspect the selected commands.
Custom presets and both clients remain supported. Inspect `help config` before
exporting a complete custom configuration.
