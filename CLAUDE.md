# CLAUDE.md / AGENTS.md

Project-specific instructions for tmux-a2a-postman.

## 1. Workflow

### 1.1. tmux-a2a-postman Workflow

After any implementation work:

- Run: `nix flake check`
- Run: `nix build`
- Both must pass before switching sessions or creating new tasks
