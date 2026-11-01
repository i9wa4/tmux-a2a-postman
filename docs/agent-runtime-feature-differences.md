# Agent Runtime Feature Differences

## 1. Shared Postman surface

Codex, Claude Code and local LLM consoles receive the same tmux text/paste/C-m
notification in their existing input editor. Postman does not call model APIs,
register conversations or start another agent. Agent-specific authentication,
permissions and input behavior remain with the agent.

| Feature          | Shared contract                                        | Runtime responsibility                                |
| ---------------- | ------------------------------------------------------ | ----------------------------------------------------- |
| Foreground mail  | send-heredoc, pop, get-status, execute-bash            | Run CLI tools in the existing role pane               |
| Notification     | Existing pane receives a text hint and C-m             | Accept text and submit; handle queued input when busy |
| Complete reading | POP returns an archive path                            | Read through EOF before acting or replying            |
| Role identity    | Verified native tmux binding and configured pane title | Preserve the role title                               |
| Command review   | Exact mail-based approval correlation                  | Reviewer decides; tool policy remains agent-owned     |
| Compaction       | Isolated Claude/Codex marker detectors retained        | No mandatory resident observer                        |

## 2. Verification and changes

Deterministic real-terminal tests cover plain and bracketed-paste consoles.
An additional real Codex TUI run uses a fixed local Responses API model fixture,
without authentication. This tests the same generic terminal transport, not a
Codex-specific input API. Actual paid-model reasoning and a real Claude model
run are not established by those fixtures.

Consult [Claude Code documentation](https://code.claude.com/docs/en/overview)
and [Codex documentation](https://developers.openai.com/codex/cli/) when
changing runtime launch/settings. Keep the shared transport independent of those
APIs. See [notification](design/notification.md) and [README](../README.md) for
the canonical behavior. Last integration review: 2026-10-31.
