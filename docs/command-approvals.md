# Command approval

Issue #937's daemon-free target is one foreground `execute-bash` call for one
Bash command:

```sh
tmux-a2a-postman execute-bash --command 'git status'
```

When the configured Approver pane is verified present, the call requests
approval and waits synchronously; it executes only after approval. When that
pane is verified absent, it executes in advisory mode and records that no
reviewer approved it.

This is the target file-first contract under validation, not a claim that the
legacy daemon cutover is complete. See [README.md](../README.md) for the
current user workflow and [file-first
architecture](design/file-first-architecture.md) for the wider design.
