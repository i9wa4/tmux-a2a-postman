# CLAUDE.md / AGENTS.md

Project-specific instructions for tmux-a2a-postman.

## 1. Workflow

### 1.1. tmux-a2a-postman Workflow

After any implementation work:

- Ensure new files are staged: `git add <new-files>` (Nix flakes only see
  git-tracked files)
- Run: `nix flake check`
- Run: `nix build`
- Both must pass before switching sessions or creating new tasks
- Check that `README.md` and `skills/*/SKILL.md` do not contain deprecated
  references (e.g., removed commands, renamed flags, deleted packages)

### 1.2. Cross-Platform Notes

- Do NOT use `/proc/{pid}` for process liveness checks — Linux-only; fails on
  macOS
  - Use `os.FindProcess` + `proc.Signal(syscall.Signal(0))` instead
  - Treat `nil` and `EPERM` as alive; `ESRCH` as dead

### 1.3. Daemon Restart Procedure

Use this when `tmux-a2a-postman stop` fails or when two daemons are running.

#### Normal restart

```bash
tmux-a2a-postman stop
tmux-a2a-postman start
tmux-a2a-postman --version   # confirm new binary hash
```

#### Two-daemon scenario (stop fails)

When two daemons are running, `stop` may silently target the wrong one.

1. Find the daemon's TUI pane (the pane running the postman dashboard).
2. Press `q` in that pane to gracefully exit the daemon.
3. Repeat for any second daemon pane.
4. Confirm no daemon is running:

   ```bash
   pgrep -f tmux-a2a-postman
   ```

   Expected: no output (exit 1).

5. Start a fresh daemon:

   ```bash
   tmux-a2a-postman start
   ```

6. Verify binary version matches HEAD:

   ```bash
   tmux-a2a-postman --version
   git rev-parse --short HEAD
   ```

   Both must show the same hash. If they differ, `nix build` and restart again.
