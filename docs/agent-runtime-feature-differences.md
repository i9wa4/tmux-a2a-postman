# Agent Runtime Feature Differences

## 1. Current surfaces

| Feature                             | Claude Code                                 | Codex CLI                                                       | Postman responsibility                                       |
| ----------------------------------- | ------------------------------------------- | --------------------------------------------------------------- | ------------------------------------------------------------ |
| Foreground mail commands            | Agent can invoke CLI                        | Agent can invoke CLI                                            | Durable files, native identity, routing, exact replies       |
| Bash-hosted notification            | Supply an executable noninteractive handler | Supplied example invokes `codex exec --sandbox workspace-write` | Readline hook, recipient registration, bounded key enqueue   |
| Existing interactive TUI activation | Unsupported                                 | Unsupported                                                     | Never submit Enter into a TUI composer                       |
| Runtime approval/sandbox            | Claude-owned settings/hooks                 | Codex-owned policy/sandbox                                      | `execute-bash` is coordination, not sandbox enforcement      |
| Compaction                          | Reusable Claude marker detector             | Reusable Codex marker detector                                  | No resident observer or automatic recovery injection         |
| Skills                              | Install with `--agent claude-code`          | Install with `--agent codex`                                    | Three packaged instruction skills; skills do not wake agents |

## 2. Updating this record

Runtime behavior is owned by the installed agent version. Consult
[Claude Code documentation](https://code.claude.com/docs/en/overview) and
[Codex documentation](https://developers.openai.com/codex/cli/) before changing
handlers or claiming runtime parity. Temporary discoveries belong here once
verified; unsupported interactive activation must remain explicit.

The Bash/Readline integration is smoke-tested without a paid model. The Codex
handler's model response depends on installed Codex configuration and access;
transport acceptance does not establish a model action. Last integration
review: 2026-10-31. See [notification](design/notification.md) and
[README](../README.md) for the canonical setup.
