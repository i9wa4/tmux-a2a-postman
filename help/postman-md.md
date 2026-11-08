# postman.md format

This installed reference defines runtime configuration, independently of the
repository's contributor `AGENTS.md`. It is usable without a dotfiles checkout.
Policy is saved in each delivered archive, not injected on console startup.

## 1. Selection and anatomy

Operational CLI selection is one absolute `--config`, then the documented
inherited `TMUX_A2A_POSTMAN_CONFIG`, then
`$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md` or
`$HOME/.config/tmux-a2a-postman/postman.md`. Relative selections fail; cwd does
not select a config. Both status getters read a saved cache and ignore
configuration. Symlinked config files, including Nix-managed ones, are
supported. Each invocation reads one bounded regular file; no TOML, overlay
chain or daemon defaults apply.

| Place                   | Valid form / default                                    | Concrete effect and boundary                                                                |
| ----------------------- | ------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| YAML `skill_path`       | Optional path, list or selected path entries            | Adds ordinary Skill discovery metadata to recipient archives, not execution.                |
| YAML `interface_node`   | Optional existing role name                             | Designates the local interface; no implicit default. Mermaid designation is also supported. |
| YAML external fields    | Optional root/workspace/session scalar strings together | Binds the named interface workspace/session to a separately provisioned file channel.       |
| YAML `presets`          | Optional named ordered columns/panes                    | Explicit `launch-agents` only; never evaluated by policy rendering or ordinary mail.        |
| H2 `edges`              | One Mermaid graph, supported subset below               | Validates allowed role routes and optional reviewer/interface designation.                  |
| H2 `common_template`    | Optional Markdown                                       | Cooperative instructions included for every intended recipient.                             |
| H2 role name            | Optional role Markdown; roles can also be graph nodes   | Includes only the selected recipient's full role contract.                                  |
| H3 `role` inside a role | Optional Markdown summary                               | Supplies its short description in contact lists; absence supplies no invented summary.      |
| H2 `message_footer`     | Optional Markdown with supported substitutions          | Additional ordinary-mail reply guidance; fixed typed execution guidance takes precedence.   |

All three reserved H2 names (`edges`, `common_template`, `message_footer`)
remain sections, not role nodes. Numbered forms such as `## 2. common_template`
with the name in backticks are accepted, as are numbered H3 `role` headings. Use
one unique section per name. Other backtick H2 names declare valid roles.

## 2. Annotated complete configuration

This self-contained example has no optional catalog dependency. The Mermaid
block defines routing; prose below the other headings is cooperative policy.

````markdown
# Example team

## 1. `edges`

```mermaid
graph LR
    manager --- worker
    manager --- reviewer
    worker --- reviewer
    class manager interface_node
    class reviewer command_approver_node
```

## 2. `common_template`

Read each POP Markdown file through EOF. Return evidence or a concrete blocker.
Keep the user's acceptance criteria attached to delegated work.

## 3. `manager`

### `role`

Coordinate this team and reconcile implementation and review evidence.

## 4. `worker`

### `role`

Implement scoped work and report checks and unresolved risks.

## 5. `reviewer`

### `role`

Review exact command requests and return the supplied approval or decline.

## 6. `message_footer`

Reply using `{reply_command} {reply_arguments}` and a quoted heredoc body.
Allowed contacts: {can_talk_to}.
````

Create consoles with unique native titles `manager`, `worker`, `reviewer`, in
one canonical workspace directory below HOME. Role prose is cooperative policy.
Routes and exact command decisions are enforced by Postman; runtime sandbox
permissions and human authorization remain separate. A review designation is
not permission to execute arbitrary prose or bypass the runtime.

The strict Mermaid subset accepts `graph`/`flowchart` directions, `---` edges,
simple labels/classes, `class` and `classDef`. It rejects other edge operators
and malformed or duplicate sections. Role names start alphanumeric, contain only
alphanumeric characters/hyphens, are at most 64 characters, and cannot be
`postman`. A class cannot invent an unknown node. There is at most one
designated reviewer and one local interface. Legacy frontmatter
`command_approver_node` is rejected; put that designation in Mermaid.
Explanatory README diagrams use broader Mermaid syntax and are not copyable
`edges` configurations.

For role descriptions, prefer `### role` with the name in backticks. Role-local
YAML scalar `role` remains a compatibility form. The full role body is
preserved; ordinary first-paragraph prose is not automatically a contact
summary.

## 3. Skill catalogs

`skill_path` accepts these alternatives in the document's YAML frontmatter:

```yaml
skill_path: /absolute/installed/skills
```

```yaml
skill_path:
  - /absolute/first/skills
  - ~/other-skills
```

```yaml
skill_path:
  - path: /absolute/installed/skills
    skills: [postman-usage, postman-send-message]
  - path: /absolute/another/skills
    skills: all
```

Each root contains Skill subdirectories with `SKILL.md` name/description
metadata. Relative paths resolve against the config directory, `~/` against
HOME. A catalog is sorted discovery metadata and source paths; it is not the
full Skill body, an installer, a tool grant or automatic execution. Install/read
Skills through the agent's ordinary mechanism. Missing roots or selected
metadata fail archive rendering explicitly. Read-only topology/status does not
open catalogs. Legacy PING-only catalogs do not become ordinary-mail catalogs,
start timers or restore startup injection.

The practical repository example uses `../../skills` from its own directory.
When copying it to XDG, replace that root with the actual installed directory;
do not assume the repository remains at a private or fixed path.

## 4. Recipient Markdown and footer substitutions

For a message to worker, the example becomes this sequence of regions:

1. Immutable message metadata, including intended sender/recipient and ID.
2. Complete common policy, selected ordinary catalog metadata, then **worker's**
   full role contract. Manager/reviewer contracts are not substituted for it.
3. `You can talk to` recipient-specific contacts and role summaries, the
   full-read requirement, and the exact config-aware reply or typed decision
   commands.
4. Applicable configured footer, then the sender's literal Markdown through EOF.

For example `{can_talk_to}` becomes worker's configured contacts and
`{reply_command} {reply_arguments}` becomes the exact config-aware command and
claimed message ID. These are single-brace substitutions, not shell expansion.
Supported placeholders are:

| Placeholder                               | Meaning                                                             |
| ----------------------------------------- | ------------------------------------------------------------------- |
| `{contacts_section}`, `{can_talk_to}`     | Recipient contact descriptions / comma-separated allowed addresses. |
| `{reply_command}`, `{reply_arguments}`    | Config-aware send command / exact reply arguments.                  |
| `{message_id}`, `{input_request_id}`      | Current message / its input request ID, if any.                     |
| `{reply_to}`, `{fills_input_request_id}`  | Referenced original message / input-request IDs for replies.        |
| `{sender}`, `{recipient}`                 | Exact current addresses, including sessions for cross-session mail. |
| `{node}`, `{session_name}`, `{timestamp}` | Recipient role, recipient session and message creation time.        |
| `{reply_policy}`                          | Current reply obligation description.                               |
| `{required_reply_completion_gate}`        | Task-evidence reminder for reply-required mail.                     |

Typed execution/external requests use their fixed decision/report guidance
rather than a generic configured reply footer. An archived sent copy still
describes the intended recipient; it does not grant its reader that role.
Changing configuration later does not rewrite existing archives.

## 5. Direct parent/child interfaces

An interface pane can address the nearest live
interface in a parent or child canonical workspace directory on the same tmux
server. Both panes must use the configured `interface_node` role. A live
interface at an intermediate directory blocks a shortcut; siblings, unrelated
roots, equal roots, and path-prefix lookalikes do not form routes. Native
session, role, pane, server, and workspace identity are checked before mail is
committed. The address remains `session:interface_node`, and exact replies use
the saved return address subject to the same current route check.

For a complete hierarchy example, see `testdata/config/hierarchy.md` in the
repository. Each session uses its own gateway in a different canonical workspace
root below HOME, with the child root below the parent root. Parent gateway sends
to `postman-child:gateway`; child gateway sends to `postman-parent:gateway`.
Other local roles use local edges. Exact replies preserve their original return
route. Siblings, grandchild shortcuts and ambiguous bindings fail. Session names
select the native recipient; canonical roots establish the relationship.

## 6. External file entry and optional launching

An external binding adds these scalar fields together to the configured
interface's frontmatter; replace the absolute private root for your machine:

```yaml
external_mailbox_root: /absolute/private/postman-mailbox
external_workspace: ~/postman-parent
external_session: postman-parent
```

This requires the declared local `interface_node`, exact native session and
canonical workspace. Provision the private inbox/outbox/read directories and
permissions before activation. The external producer explicitly invokes
`channel-sync` after publishing files and on reconnect. The interface POPs and
uses the generated report/decision guidance. Declaration alone starts no service
and grants no host permissions. Installed external operating instructions live
in `tmux-a2a-postman help postman-usage external`.

Optional YAML `presets` maps names to `columns`, each with ordered `panes`.
Agent panes require an existing unique `role` and exact shell `command`. A
title-only shell pane has a unique non-role `title`, no command, and no mail
recipient identity. At least one agent role is required. `launch-agents --list`
and `launch-agents <preset> --dry-run` are inert; only explicit launch starts
shells and commands in a new window of the verified invoking session, or a
dedicated session outside tmux or with an explicit target. New consoles inherit
the absolute config selection described above. No startup policy mail,
credentials, model flags or supervisor are added. Use installed
`help launch-agents` for selection, shell initialization, collisions and
partial-start cleanup.

## 7. Bounds and unsupported forms

Configuration is at most 1 MiB with lines at most 64 KiB, 256 roles and 1024
edges. Ordinary catalogs have at most 32 roots, 256 selected Skills per root,
256 combined Skills and 32 KiB generated metadata. Oversized content fails
explicitly; archive payloads also have the CLI's documented bounds. Presets have
at most 32 names, 8 columns and 64 panes, with 8192-byte UTF-8/NUL-free
commands. Unknown YAML is not an executable extension mechanism. Legacy TOML
overlays, daemon policy keys, notifier registration and automatic PING are not
setup.

Receiving roles must be existing consoles accepting text/paste and `C-m` in
uniquely titled panes. Codex's `tui.terminal_title = []` preserves titles. Shell
prompts and tmux copy/input modes are not receiving consoles. Notification
failure leaves mail durable and never grants command approval. Consult installed
`help config`, `help send-heredoc` and `help execute-heredoc` for exact
commands.

## 8. Builtin standard configuration

An absent automatic XDG/HOME path selects the embedded complete standard team. A
custom file replaces it completely; there is no implicit role/catalog merge. Use
`tmux-a2a-postman help config` for precedence, error handling and explicit
read-only inspection/export of the installed default.
`help postman-orchestrate`, `help postman-plan`, `help postman-records`
and `help postman-review` own its portable workflows.
