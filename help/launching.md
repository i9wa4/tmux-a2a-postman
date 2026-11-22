# Optional agent-team presets

`launch-agents` expands one explicitly selected preset into a new tmux window in
the invoking session, or into a dedicated session when no invoking pane is
available, and exits. Ordinary manually started consoles remain supported.
Reading configuration, sending mail, POP, and refreshing status never launch
consoles. There is no daemon, startup role message, PING, supervisor, or respawn
loop.

## 1. Configuration and preview

The default `postman.md` contains startup commands in its YAML `presets`,
alongside routing edges and Markdown cooperation policy in the same file. Each
preset has ordered `columns`, each containing top-to-bottom `panes`. An agent
pane has an existing `role` and explicit shell `command`. A title-only pane has
a unique non-role `title` and no command; it starts an ordinary interactive
shell and is not a mail recipient. A role appears at most once in a preset;
different presets may reuse it. At least one role pane is required. Limits are
32 presets, 8 columns, and 64 panes per preset. Unknown nested keys and invalid
roles, titles or commands are rejected. Commands are not rendered into
recipient policy or evaluated by config readers.

```sh
tmux-a2a-postman launch-agents --list
tmux-a2a-postman launch-agents dev --dry-run
tmux-a2a-postman launch-agents preset-p \
  --socket /absolute/private/tmux.sock --cwd /absolute/team-directory --dry-run
```

Both commands are inert: no tmux creation, state registration, command
execution, or persistent launch logs. Review the exact shell source in the
preview. Remove `--dry-run` to run it. The new socket's parent directory must
already exist.

## 2. Cwd, server, configuration, and shell

Without `--socket`, the explicit native server in `TMUX` is selected. When
`TMUX_PANE` identifies a verified pane on that server, a plain invocation
creates a new window in that pane's session. An invalid supplied pane fails
before mutation. Without a pane, the default dedicated session name is derived
from preset and root. Explicit `--socket` retains dedicated-session behavior;
`--session` names a dedicated session to create, not an existing target to
modify. No existing pane is renamed, split, moved, or closed.

Without `--cwd`, the invocation directory is used. It must resolve below
`HOME` and is the exact cwd for every new pane; its canonical HOME-relative
root identifies the workspace.

The consoles inherit **`TMUX_A2A_POSTMAN_CONFIG`**, the absolute selected config
path. Operational CLI selection is explicit `--config`, then that inherited
variable, then `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md` (or the HOME
default). The existing server's global environment is unchanged. A child command
that clears its environment must preserve the variable or pass `--config`
itself. Both status getters remain cache-only and ignore config selection.

The existing server's `default-shell` is used; on a new server, `SHELL` or
`/bin/sh` is used. Supported shell validation is shared with command execution.
Each role command is passed intact as one argument to `shell -lic`: normal login
and interactive initialization runs, including aliases and functions defined
there. A title-only pane starts `shell -il` with no declared command.
First-prompt-only hooks and a running shell's private state are not reproduced.
For environment loading that needs a prompt hook, declare an explicit command,
for example `direnv exec . codex`; Postman never approves `.envrc` itself.
Client/model/permission arguments are the exact declared preset commands. The
launcher adds no credentials or provider flags.

## 3. Outcomes and ownership

The launcher first creates inert native panes, establishes cwd, pane titles,
inherited config and normal verified workspace ownership, and only then starts
ordinary shells and declared role commands. It then refreshes registered
configuration provenance against a live role's post-start pane PID. A title-only
shell is not registered as a communication role. The first pane is focused. JSON
reports actual server/session/window/pane IDs and accepted role command starts.
This establishes neither authentication nor readiness; a configured command can
exit immediately. Successful exit leaves the team alive.

A nonblocking OS advisory lock serializes setup for one canonical workspace.
Repeated targets, existing dedicated target sessions, live/unverified workspace
owners, and existing configured role panes are refused. Scoped private receipts
under the normal state root record each attempt; logs are retained until the
user deletes them. Existing state-root mode 0755 is accepted; children remain
private.

An ordinary error or interrupt removes only a newly created session or window
whose native server incarnation, IDs, and unpredictable ownership token still
match. Borrowed servers, sessions and unrelated windows remain alive. Already
started commands may have irreversible effects; closing their panes does not
undo those effects or guarantee that detached descendants have stopped. The
output and receipt report partial/uncertain starts and any cleanup that could
not be verified. Those targets are never automatically replayed. Only a recorded
pre-command failure with verified removal permits a subsequent explicit attempt
at that target. A killed launcher can leave resources and uncertain receipts;
inspect them manually. There is no force-unlock, automatic repair, or lifecycle
registry.

## 4. Verification

Native Go tests create isolated servers from the launcher, then drive the shared
deterministic terminal fixture through receiver-owned POP, full Markdown
reading, correlated replies, and execution approval. Additional tests cover
quoting, non-default config continuity, concurrent setup, native collisions,
cancellation, lost creation responses, and partial starts without replay. They
use no model credentials or live user sessions. See
[contributor checks](https://github.com/i9wa4/tmux-a2a-postman/blob/main/docs/development/contributing.md).

For filesystem references, explicitly run `tmux-a2a-postman resources` once
and use the returned absolute directory as `postman_resources`. See
`tmux-a2a-postman help resources` for offline prerequisites and integrity.

## 5. Builtin presets

When no automatic user configuration exists, `launch-agents --list` lists three
presets from the embedded default `postman.md`:

- `dev`: the original two equal-width columns, with a focused `editor` shell
  on the left and one `agent` role on the right. Only the right pane runs the
  original explicit Codex command. `editor` is a native title, not a message
  recipient.
- `preset-p`: the existing vde-layout Codex team in two columns of four and
  three roles.
- `preset-w`: the same seven-role layout with the existing mixed Codex/Claude
  command mapping.

The original VDE `dev` uses `current-window`; Postman creates a new window in
the verified invoking session, or a dedicated session outside tmux. It never
rewrites an arbitrary existing window. The seven-role presets preserve their
original model, effort and permission arguments. Preview with `--dry-run` to
inspect the exact declared commands.
All presets are in that one default file; no separate preset configuration is
needed. A custom `postman.md` remains a complete replacement. See `help config`
to inspect/export the default and `help postman-orchestrate` for the workflow.
