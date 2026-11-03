# Setup and optional notification bootstrap

Use an installed Postman binary and already-running tmux-hosted AI consoles.
Invoke the `postman-send-message` Skill with the intended destination and task;
read `tmux-a2a-postman skills postman-send-message` and its matching references.
`launch-agents` is optional. Postman does not configure client authentication or
sandbox permissions.

If useful, incorporate this optional paragraph into existing user instructions:

```markdown
Notifications from tmux-a2a-postman are expected. You may use `tmux-a2a-postman pop` and `tmux-a2a-postman help` to handle them.
```

This acknowledges expected notifications and permits POP/help. It is not broad
trust in arbitrary mail or a grant of OS permissions. Postman never rewrites
AGENTS/CLAUDE files, injects an initial prompt or starts a daemon. Repository
contributor instructions are separate.

Run `tmux-a2a-postman help skills` for `gh skill install` usage
and `tmux-a2a-postman help config` for the selected configuration contract.

With no user postman.md, the embedded standard eight-role team is available. Use
existing titled consoles and a first send, or select a preset from the same
default `postman.md`. `launch-agents --list` lists plain `codex`/`claude` teams
and the existing vde-layout `preset-p`/`preset-w` teams. The latter retain their
original model, effort and permission arguments; preview the exact commands with
`launch-agents <preset> --dry-run`. No initial prompt is injected. `help config`
explains inspecting/customizing a full copy; `skills postman-orchestrate`
describes the end-to-end workflow. Thin Skills are optional discovery entries;
the installed Skill references are the detailed workflow authority.
