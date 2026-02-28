# CLAUDE.md

Project-specific instructions for tmux-a2a-postman.

## 3. Workflow

### 3.5. tmux-a2a-postman Workflow

After any implementation work:

- Run: `nix flake check`
- Run: `nix build`
- Both must pass before switching sessions or creating new tasks
