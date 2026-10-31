# Agent Runtime Feature Differences

## 1. Current surfaces

| Feature                             | Claude Code                      | Codex CLI                                         | Postman responsibility                                     |
| ----------------------------------- | -------------------------------- | ------------------------------------------------- | ---------------------------------------------------------- |
| Foreground mail commands            | Agent invokes CLI                | Agent invokes CLI                                 | Durable files, native identity, routing, exact replies     |
| Existing interactive TUI activation | Unsupported                      | Native queue for registered existing conversation | Short-lived metadata/queue stdio client; no composer input |
| Optional Bash automation            | Executable handler               | Executable handler                                | Readline hook and native binding                           |
| Runtime approval/sandbox            | Claude-owned                     | Codex-owned                                       | execute-bash coordinates review; it is not a sandbox       |
| Compaction                          | Reusable marker detector         | Reusable marker detector                          | No resident observer or recovery injection                 |
| Skills                              | Install with --agent claude-code | Install with --agent codex                        | Three skills; recipient registration is also required      |

## 2. Version and evidence

Codex CLI 0.162.1 and 0.163.0 expose the tested `thread/read` and
`thread/queue/add` app-server methods. Their native external queue watcher
works with `--no-daemon`; idle dispatch can take a ten-second watcher interval.
The real TUI and stdio helper are tested against a deterministic local Responses
API fixture, without authentication or model access. This proves notification
and tool execution in the original conversation, not a real model's reasoning.

See [notification](design/notification.md) and [README](../README.md) for the
canonical contract/setup, and [Contributing](../CONTRIBUTING.md) for the test
command. Runtime behavior belongs to the installed version; consult
[Claude Code documentation](https://code.claude.com/docs/en/overview) and
[Codex documentation](https://developers.openai.com/codex/cli/) before claiming
new capabilities. Last integration review: 2026-10-31.
