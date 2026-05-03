# CLAUDE.md / AGENTS.md

Project-specific instructions for tmux-a2a-postman.

## 1. Development Checks

### 1.1. After Implementation

After any implementation work:

- Ensure new files are staged: `git add <new-files>` (Nix flakes only see
  git-tracked files)
- Run: `nix flake check`
- Run: `nix build`
- Both must pass before switching sessions or creating new tasks
- Check that `README.md` and `skills/*/SKILL.md` do not contain deprecated
  references (e.g., removed commands, renamed flags, deleted packages)

### 1.2. Go Dependency Changes

When changing Go dependencies, `go.mod`, `go.sum`, Go versions, or
`vendorHash`:

- Run `go mod tidy`
- Run `nix build --option substitute false --print-build-logs`
- If Nix reports a `vendorHash` mismatch, copy the reported `got:` hash into
  `flake.nix` and rerun the build

## 2. Runtime Portability

### 2.1. Process Liveness

- Do NOT use `/proc/{pid}` for process liveness checks — Linux-only; fails on
  macOS
  - Use `os.FindProcess` + `proc.Signal(syscall.Signal(0))` instead
  - Treat `nil` and `EPERM` as alive; `ESRCH` as dead

## 3. Daemon Operations

### 3.1. Restart Procedure

Use this when `tmux-a2a-postman stop` fails or when two daemons are running.

#### 3.1.1. Normal restart

```bash
tmux-a2a-postman stop
tmux-a2a-postman version   # inspect the resolved binary version
tmux-a2a-postman start
```

#### 3.1.2. Two-daemon scenario (stop fails)

When two daemons are running, `stop` may silently target the wrong one.

1. Find the daemon's TUI pane (the pane running the postman dashboard).
2. Press `q` in that pane to gracefully exit the daemon.
3. Repeat for any second daemon pane.
4. Confirm no daemon is running:

   ```bash
   pgrep -f tmux-a2a-postman
   # Expected: no output (exit 1)
   ```

5. Start a fresh daemon:

   ```bash
   tmux-a2a-postman start
   ```

6. Verify binary version matches HEAD:

   ```bash
   tmux-a2a-postman --version
   git rev-parse --short HEAD
   ```

   If the version JSON includes a concrete `commit`, it should match HEAD. If
   the commit is `unknown`, the binary was likely built from a dirty/local flake
   evaluation. For strict HEAD confirmation, clean the worktree, run
   `nix build`, and restart again.

## 4. Release Hygiene

- The tag-push release workflow owns GitHub Release creation via GoReleaser
- Do not run `gh skill publish --tag` inside the tag-push workflow; that command
  tries to create the tag/release itself and fails when the pushed tag already
  exists
- Use `nix run '.#skill-check'` for CI validation of `skills/*/SKILL.md`
- Keep exactly one release owner per flow:
  - Manual skill publishing flow: `gh skill publish --tag ...`
  - Repository tag-push flow: pushed `v*` tag + GoReleaser
- If a tag workflow fails because the workflow file at that tag is broken, push
  the fix to `main` and create a new tag; rerunning the old tag usually reruns
  the old workflow definition
