# First exchange and operations

This guide describes the current source checkout. Use a binary built from that
checkout, as shown in [README](../README.md); check `version` and
`help commands` before following instructions with an older release.

## 1. Set up two consoles

From the checkout, copy the complete minimal configuration:

```sh
postman_config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/tmux-a2a-postman"
mkdir -p "$postman_config_dir"
cp examples/minimal/postman.md "$postman_config_dir/postman.md"
mkdir -p "$HOME/postman-demo"
```

`XDG_CONFIG_HOME`, when set, must be absolute. The file is selected by explicit
absolute `--config`, inherited `TMUX_A2A_POSTMAN_CONFIG`, then the XDG/HOME
default. For this default-path walkthrough, unset any inherited
`TMUX_A2A_POSTMAN_CONFIG` before creating consoles. No config is discovered by
walking up from cwd.

Choose an unused demo socket name. These commands create a separate tmux server
and two ordinary shells; they do not acquire agent credentials or send mail:

```sh
unset TMUX_A2A_POSTMAN_CONFIG
orchestrator_pane=$(tmux -L postman-demo -f /dev/null new-session -d \
  -s postman-demo -c "$HOME/postman-demo" -P -F '#{pane_id}')
worker_pane=$(tmux -L postman-demo split-window -h -d -t "$orchestrator_pane" \
  -c "$HOME/postman-demo" -P -F '#{pane_id}')
tmux -L postman-demo select-pane -t "$orchestrator_pane" -T orchestrator
tmux -L postman-demo select-pane -t "$worker_pane" -T worker
tmux -L postman-demo attach -t postman-demo
```

Start your chosen agent console in each shell. Its tool subprocesses must find
`tmux-a2a-postman` on `PATH`, and preserve the native `TMUX`/`TMUX_PANE`
context. Keep the titles `orchestrator` and `worker`; for Codex,
`tui.terminal_title = []` disables automatic title replacement. Authentication,
sandbox and tool access are the client's responsibility. A plain shell prompt
cannot accept Postman's agent notification. No initial role message is injected;
instructions arrive with each real message.

Both agent panes must run in the same canonical directory below HOME. That
native pane cwd selects the workspace; a tool's temporary `cd` does not.
Separate worktrees are separate workspaces. An independent live session cannot
also own that same workspace; unknown ownership is not silently replaced.

## 2. Send, POP, read, reply

Have the orchestrator console run:

```sh
tmux-a2a-postman send-heredoc --to worker <<'POSTMAN_BODY'
Please confirm you can read this complete message and reply with the result.
POSTMAN_BODY
```

Worker receives a short input hint followed by Enter. Worker must run
`tmux-a2a-postman pop`, read the **entire** file at `markdown_absolute_path`
through EOF, and follow its exact reply command. An empty POP returns a null
path. Do not pass a message ID to POP.

For example, the returned guidance uses `send-heredoc --to` with the claimed
message ID and a quoted heredoc body. Use the actual ID and config-aware command
from that archive; do not reply to the role name when closing this request.
Orchestrator then POPs and fully reads the correlated reply. Sending to a role
creates a reply obligation; replying to a claimed ID does not create another
obligation. The [CLI specification](cli-spec.md) defines output fields, multi-ID
replies and partial-delivery recovery.

The archive contains recipient-specific common policy, catalog metadata and role
instructions, `You can talk to` contacts, exact reply guidance, then the literal
sender body. Sent copies describe the recipient; reading one never changes the
reader's role or grants its authority. Existing archives keep the policy saved
at delivery, even after the config changes.

## 3. Customize the team

Use the complete examples as separate starting points:

| Example                                       | Setup and what to change                                                                                                                                             |
| --------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [Minimal](../examples/minimal/postman.md)     | Two roles; no optional files, catalog or designated approver.                                                                                                        |
| [Practical](../examples/practical/postman.md) | Messenger, manager, builder, reviewer and approver. Use an absolute `--config` to this file in place; copied configs need an edited absolute installed catalog root. |
| [Hierarchy](../examples/hierarchy/postman.md) | Both `postman-parent` and `postman-child` use the same complete file, each with gateway/builder panes, in distinct workspace roots.                                  |
| [Launch](assets/examples/launch/postman.md)   | Optional shell or AI presets; preview before explicit launch. Client installation and readiness remain yours.                                                        |

Add a role by adding its H2 section, native pane title and intended `---` edges.
Change shared instructions in `common_template`; change a role's contract only
in that role section. Its `### role` summary is used for contacts. Change Skill
discovery in YAML `skill_path`; it supplies names/descriptions, not automatic
Skill execution. See the [anatomy and field reference][config-reference].

For the hierarchy example, gateway in `postman-parent` can send to
`postman-child:gateway`, and the child gateway can send to
`postman-parent:gateway`. Local builder uses its local gateway; no sibling or
grandchild shortcuts are implied. The declaration does not create sessions.
For a worked deterministic development trial, see the
[recursive-session guide](recursive-sessions-trial-runbook.md).

To add an external entry, first provision a distinct private inbox/outbox/read
root using the
[external channel contract](design/external-filesystem-channel.md). For the
hierarchy example's top-level gateway, add these scalar YAML fields to the
existing frontmatter (replace the absolute mailbox path for your machine):

```yaml
external_mailbox_root: /absolute/private/postman-mailbox
external_workspace: ~/postman-parent
external_session: postman-parent
```

The interface's actual workspace must match `~/postman-parent`. The external
producer publishes literal files and explicitly invokes bounded `channel-sync`
after publication and on reconnect. The interface POPs normally and reports
actual results or exact pending decisions using `channel-report-heredoc`.
There is no Postman polling service or native GitHub adapter.

## 4. Observe without inventing progress

`refresh-status` attempts one finite server-wide collection. Busy refresh skips
immediately. `get-status` reads its saved JSON unchanged; `get-status-oneline`
renders the same snapshot using actual native numbers. Getters do not query
tmux, scan mail or create state. The selected server comes from explicit native
`TMUX`; an operator-wide snapshot does not grant access to all visible roles.

Eligible send and POP operations attempt the same shared refresh after their
mail locks are released. Per-pane capture cadence, coverage and timestamps
distinguish stale/incomplete evidence. Notification safety separately uses live
recipient-only captures. Screen changes prove neither a POP nor task completion.
Logs and captures remain until you delete them; there is no expiry service.
See [server status](design/server-status.md) for scope, diagnostics and external
status-line composition.

Notifications serialize Postman writers for the destination pane. A doubtful
submission leaves an input guard; subsequent writers do not blindly paste or
repeat Enter. Copy mode and shell prompts are rejected. This does not preserve
unsent human drafts or exclude unrelated input writers. An uncertain hint never
deletes durable mail, and a lost command response is not permission to resend.
Inspect the recorded outcome and recover using the CLI's exact error guidance.

## 5. Review commands and recover context

`execute-heredoc --reason 'why'` receives exact source on stdin. A verified
present approver receives the command, reason, cwd and shell for full reading;
it chooses `approve-execution` or `decline-execution` with the exact request ID.
The requester waits for that decision and executes in its initialized shell.
An absent/unconfigured approver permits advisory execution; unknown identity,
rejection or timeout never turns into advisory permission. This is coordination,
not sandbox enforcement. See [command review](command-approvals.md) for
environment, stream, timeout and uncertain-execution contracts.

The shipped [Skills](../skills/) cover sending, usage, configuration audit,
recall and team management. Install them through the client's normal Skill
mechanism from the matching source/version. Required operating references stay
inside the Skill directories; a separate dotfiles checkout is unnecessary.
Recall reads recorded history without POP or send. Team management checks
direct-report evidence against the user's objective; neither requires a
particular tracker or treats an acknowledgement as completion.

## 6. Documentation and evidence

[CLI specification](cli-spec.md), [configuration reference][config-reference],
command approval, notification, server-status and external-channel contracts
describe current supported behavior. `docs/validation/` retains dated test
evidence; historical designs and the
[agent-environment comparison](research/891-agent-environment-comparison.md)
describe investigations, retired proposals or future experiments, not setup
requirements. [CONTRIBUTING](../CONTRIBUTING.md) owns test procedures and
limits; [RELEASING](../RELEASING.md) owns publication. This repository's
`AGENTS.md` guides development and is distinct from your runtime `postman.md`.

[config-reference]: help/postman-md.md
