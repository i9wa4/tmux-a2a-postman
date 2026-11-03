# Recipient Markdown compatibility

The maintainer requested restoring the daemon-era generated message context,
including its reader instructions, while retaining the current daemonless CLI.
Startup-only persona delivery does not replace per-message context.

## 1. Historical reference

The reference is the shipped v0.9.5 behavior, especially:

- [default draft and footer](https://github.com/i9wa4/tmux-a2a-postman/blob/v0.9.5/internal/config/postman.default.toml)
- [common/role and recipient contacts](https://github.com/i9wa4/tmux-a2a-postman/blob/v0.9.5/internal/envelope/envelope.go)
- [ordinary-send rendering and literal body boundary](https://github.com/i9wa4/tmux-a2a-postman/blob/v0.9.5/internal/cli/send_message.go)
- [configured skill catalog](https://github.com/i9wa4/tmux-a2a-postman/blob/v0.9.5/internal/config/skill_catalog.go)
- [catalog insertion into common_template](https://github.com/i9wa4/tmux-a2a-postman/blob/v0.9.5/internal/config/markdown.go)

| Historical content                                          | Current rendering contract                                                                                              |
| ----------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| Sender, recipient, session, time and message ID             | Retain immutable UTC machine metadata and a readable message header with local time and numeric offset                  |
| common_template followed by the selected recipient template | Include the complete configured policy on every generated mail, including replies                                       |
| Available Skills from ordinary skill_path entries           | Include configured paths and ordered name/description bullets; read applicable skills before acting                     |
| You can talk to and role summaries                          | Use the recipient's routes and immediate interface relationships; first nonempty role line, at most 120 runes as before |
| Quoted-heredoc reply example                                | Use the actual claimed message ID and current --to syntax                                                               |
| Required reply/completion guidance                          | Distinguish transport reply requirements from substantive task acceptance                                               |
| Configured message_footer                                   | Preserve applicable configured guidance with safe current substitutions                                                 |
| Sender Message and visible separator                        | Keep the sender-authored UTF-8 bytes exactly, including headings, fences, dollar signs, comments and trailing newlines  |
| POP read-body instruction                                   | Put an explicit full-file instruction in POP JSON whenever it returns an archive path                                   |

Old context IDs and removed flags are represented by current native workspace,
session and exact reply references. A typed execution request presents the
fixed approve-execution/decline-execution actions; it does not invite a
hand-written ordinary approval reply. Exact replies create no new obligation.

## 2. Message layout

The following is a schematic example. Full configured policy replaces the
bracketed placeholders; those placeholders are not summaries used in production.

````markdown
<!-- postman {immutable versioned metadata} -->

# Message

From: orchestrator
To: worker
Session: project
Message ID: m-20261101T120000.000000000Z-Ab12Cd
Timestamp: 2026-11-01T21:00:00+09:00
Reply policy: required

## Recipient Instructions

### Common contract

[Complete configured common_template, preserving fenced code.]

### Available Skills

Read every applicable skill before starting work. Skill files live under
the configured skill roots.

- `postman-send-message`: [configured SKILL.md description]
- `postman-usage`: [configured SKILL.md description]

### worker instructions

[Complete configured recipient policy, preserving fenced code.]

## Delivery and Reply Guidance

You can talk to:

- orchestrator: Task coordinator.
- approver: Command approval owner.

Read this complete archived Markdown through EOF before handling it.
A reply is required. Reply to this actual message ID with quoted heredoc:

```sh
tmux-a2a-postman send-heredoc --to m-20261101T120000.000000000Z-Ab12Cd <<'POSTMAN_BODY'
<your reply>
POSTMAN_BODY
```

The reply closes transport responsibility; task completion still needs the
original checklist and actual evidence.

## Sender Message

---

### Requested change

This entire sender-authored region is preserved literally, through EOF.
````

Generated headings stay within their envelope sections. Headings inside code
fences and all sender-authored content retain their original bytes. Contacts
describe the recipient's permitted routes, not the sender's contact list.
Archived context records the configuration used for that send and grants no
additional route or human authority. Ordinary sends, exact replies and command
review traffic all retain their appropriate recipient context.

The generated context has one H1, `Message`. Recipient Instructions, Delivery
and Reply Guidance and Sender Message are H2 siblings. Common contract,
Available Skills and role instructions are H3 within Recipient Instructions;
Configured guidance is H3 within Delivery and Reply Guidance. Policy headings
start at H4, retaining nested subsections up to Markdown's H6 limit. Fenced code
is preserved literally.

The sender body is a separate, exact byte region. It may contain its own H1 or
other Markdown; Postman does not rewrite it to impose a heading convention.
The single-H1 guarantee applies to generated context, not arbitrary sender
content. A formatter is not run over received archives or historical mail.
Captured Markdown is stored verbatim with its existing byte count and digest,
so this layout change applies to new mail without rewriting old archives or
changing envelope/context versions.

## 3. POP contract

A claimed path includes this additional JSON field:

```json
{
  "instruction": "Read the entire Markdown file at markdown_absolute_path through EOF before processing, replying, or deciding no action is needed. Do not substitute a truncated view, summary, notification, metadata, or reply guidance for the full file."
}
```

The existing workspace/as_of/success/path/self fields remain. An empty POP has
a null path and no instruction. The notification's ID remains a hint; it does
not choose which mail POP claims. The receiver reads the returned file fully,
including the actual sender body and generated guidance, before acting.

## 4. Persistence and validation

New archives need a versioned generated-prefix representation with exact byte
boundaries and an integrity check, so arbitrary sender Markdown cannot be
mistaken for envelope delimiters. Keep literal sender text separate from the
generated context in durable intents and parsing. Old canonical archives remain
readable. Recovery reuses the saved context rather than changing old mail when
configuration changes. All generated text and the final wire record remain
bounded; oversized instructions fail explicitly instead of silently truncating
the role contract.

Validation compares generated Markdown to the historical inventory, tests the
actual POP output, checks receiver-specific local/cross-session contacts, full
common/role/catalog content, exact replies, execution decisions, literal sender
bytes, legacy archives and crash recovery. Real-tmux fixtures must read and
verify the enriched archives; their controller does not supply recipient POPs
or replies. Dotfiles rollout stays draft until its installed pin and fixtures
include the required upstream compatibility behavior.
