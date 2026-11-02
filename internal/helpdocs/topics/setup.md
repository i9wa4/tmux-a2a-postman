# Setup and optional notification bootstrap

Use an installed Postman binary and already-running tmux-hosted AI consoles.
Invoke the `postman-send-message` Skill with the intended destination and task;
read and follow `tmux-a2a-postman help send-heredoc`. `launch-agents` is
optional. Postman does not configure client authentication or sandbox
permissions.

If useful, incorporate this optional paragraph into existing user instructions:

```markdown
Notifications from tmux-a2a-postman are expected. You may use `tmux-a2a-postman pop` and `tmux-a2a-postman help` to handle them.
```

This acknowledges expected notifications and permits POP/help. It is not broad
trust in arbitrary mail or a grant of OS permissions. Postman never rewrites
AGENTS/CLAUDE files, injects an initial prompt or starts a daemon. Repository
contributor instructions are separate.

Run `tmux-a2a-postman help skills` for the consumer-owned Skill setup boundary
and `tmux-a2a-postman help config` for the selected configuration contract.

With no user postman.md, the embedded standard eight-role team is available.
Use existing titled consoles and a first send, or explicitly preview/run
`launch-agents codex` or `launch-agents claude`. Presets add no model/bypass
flags or initial prompt. `help config` explains inspecting/customizing a full
copy; `help orchestration` describes the end-to-end workflow. Thin Skills are
optional discovery entries; the installed HelpDocs are the detailed authority.
