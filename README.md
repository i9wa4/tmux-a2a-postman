# tmux-a2a-postman

`tmux-a2a-postman` coordinates local AI coding agents in tmux panes through
private workspace mail files. Issue #937's file-first command path is under
development and has not passed its complete acceptance and review gates.

Any AI coding agent that can run commands in a tmux pane can use the same
`send-heredoc`, `pop`, `get-status`, and `execute-bash` commands. These four
commands derive workspace identity from the native tmux server, session and
pane, then use a configured `postman.md` for roles and Mermaid edges. They do
not require a running Postman daemon.

## 1. Install and configure

Prerequisites are macOS or Linux, Go, and tmux. Install the binary with:

```sh
go install github.com/i9wa4/tmux-a2a-postman@latest
```

For the development branch, create
`$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md`, or use
`~/.config/tmux-a2a-postman/postman.md` when XDG_CONFIG_HOME is unset.
Each of the four commands also accepts an absolute `--config` or
`--postman-md` path. The default and overrides are independent of the
caller's current directory. A missing, unreadable, or ambiguous topology
fails closed.

This is illustrative configuration syntax, not runtime output:

````markdown
## `edges`

```mermaid
graph LR
    orchestrator --- worker
    orchestrator --- reviewer
    class orchestrator interface_node
    class reviewer command_approver_node
```

## `orchestrator`

Coordinate work and accept evidence.

## `worker`

Implement assigned work and report evidence.

## `reviewer`

Review command requests and reply with an exact decision.
````

Run each role in a tmux pane with its role as the pane title. The parser
accepts a strict Mermaid subset: `---` declares an edge. Only configured
adjacent nodes can exchange mail. Other topology sections can hold role
instructions. `interface_node` is optional; marking a reviewer with
`command_approver_node` enables blocking command approval.

Packaged Codex or Claude Code skills can be installed with `gh skill install`
for agent instructions:

```sh
gh skill install i9wa4/tmux-a2a-postman postman-usage --agent codex --scope user
gh skill install i9wa4/tmux-a2a-postman postman-send-message --agent codex --scope user
gh skill install i9wa4/tmux-a2a-postman postman-config-auditor --agent codex --scope user
```

Use `--agent claude-code` for Claude Code. The skills explain safe sending,
mailbox operation, and configuration auditing respectively.

Before activating the file-first path for an existing installation, identify
one state root, UID, tmux socket/session, and recipient pane. Inventory and
disable every old daemon launch path that can reach that mail or approval state
or that recipient pane, including an explicit `--config` path, launcher-only
`POSTMAN_HOME`, and alternate `XDG_STATE_HOME` or tmux socket/session settings.
Verify every such daemon has exited before new writes; check pending mail and
approval requests first. The file-first path checks that its selected state
root matches the auto-discovered legacy configuration and uses the legacy lock
inode for same-root exclusion. Verify the new route with a private recipient
before switching production delivery. For rollback, stop new writers first,
then restore the old launcher and route; preserve existing mail and do not
replay an uncertain send. Installed alternate selectors and migration remain
tracked separately.

## 2. Send and receive

These commands are illustrative syntax, not a recorded run:

```sh
tmux-a2a-postman send-heredoc --to worker --reply-required <<'POSTMAN_BODY'
Implement the change and report the result.
POSTMAN_BODY

tmux-a2a-postman pop
tmux-a2a-postman get-status
```

`send-heredoc` reads literal quoted-heredoc stdin, verifies the local edge,
then synchronously publishes separate private receiver-inbox and sender-sent
copies. Its JSON includes the committed `message`, file-derived `self`
state, and a separate `notice` result. Native pane-input notification is
disabled because it could submit text already typed by the recipient.
`notice.status=unavailable` reports that limitation without reversing
committed mail. Neither mail nor a notice should be blindly retried after an
uncertain result.

`pop` atomically claims one own unread message and returns an openable
`markdown_absolute_path` plus its `pop_receipt_absolute_path`. The body is
not embedded in JSON. Read the complete archived Markdown through EOF before
handling the message. A successful claim is not repeated; an empty POP still
returns `self` state. An optional exact message ID selects only that
eligible own message.

Replies use the exact `--reply-to <message-id>` and
`--fills-input-request-id <input-request-id>` from the original required
message. `--reply-required` opens an obligation; `--no-reply` does not.
Filling an input request closes transport, not task acceptance. A completion
report should carry `Task artifact:`, `Original checklist: PASS`,
`Evidence:`, and `Remaining blockers: none`. Use `BLOCKED` with
`Original checklist: FAIL` while requirements remain. Receivers verify
the checklist status, durable references, evidence, and blockers before
relaying, approving, or closing work.

The `self` object reports known/unknown state, workspace and role, unread
count, exact inbound/outbound obligations, verified local peers, next action,
and observation time. `get-status` reads this saved-file state without
claiming mail or capturing panes. Missing or damaged evidence is unknown,
not zero work.

## 3. Execute a reviewed command

This command is illustrative syntax, not a recorded run:

```sh
tmux-a2a-postman execute-bash --command 'go test ./...'
```

A configured, live `command_approver_node` receives an exact
reply-required request. The requester waits up to 300 seconds for a matching
reply from that reviewer. Its body is JSON with `version: 1`, the exact
`command`, and `decision` set to `approved` or `rejected`; the reply must
also fill the request's exact input ID. Unrelated inbox mail is left alone.
Only verified reviewer absence permits advisory execution. Ambiguous
configuration or a lost reviewer fails closed. Bash runs outside the mailbox
lock; command stdout/stderr stream normally, and Postman writes result JSON
to stderr. A nonzero Bash exit is reported as `exited`. The wrapper coordinates
review and audit; it does not sandbox Bash.

After the approval request commits, `execution.notice` separately reports
the notification outcome. Native pane-input notification is disabled and
reports `unavailable`; this never grants approval or creates another request.
The reviewer must POP and send the exact decision.

## 4. Optional pane evidence

`send-heredoc` and `pop` accept `--capture-related-panes`. Native pane-text
capture currently fails closed with explicit unknown evidence and no text:
mutable tmux titles cannot prove continuous role attribution during a read.
Positive native capture and its trust boundary remain open in issue #937.
Capture failure is supplemental and never reverses committed mail.

The active command help is `tmux-a2a-postman help send-heredoc`,
`help pop`, `help get-status`, and `help execute-bash`. See
[postman.md syntax](skills/postman-config-auditor/references/postman-md.md)
and
[agent runtime feature differences](docs/agent-runtime-feature-differences.md)
for further context. Claude Code and Codex CLI have different runtime surfaces
outside postman.
