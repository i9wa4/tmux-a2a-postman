# CLAUDE.md / AGENTS.md

Project-specific instructions for tmux-a2a-postman.

## 1. Workflow

### 1.1. tmux-a2a-postman Workflow

After any implementation work:

- Ensure new files are staged: `git add <new-files>` (Nix flakes only see git-tracked files)
- Run: `nix flake check`
- Run: `nix build`
- Both must pass before switching sessions or creating new tasks
- Check that `README.md` and `skills/*/SKILL.md` do not contain deprecated references
  (e.g., removed commands, renamed flags, deleted packages)

### 1.2. Cross-Platform Notes

- Do NOT use `/proc/{pid}` for process liveness checks — Linux-only; fails on macOS
  - Use `os.FindProcess` + `proc.Signal(syscall.Signal(0))` instead
  - Treat `nil` and `EPERM` as alive; `ESRCH` as dead
