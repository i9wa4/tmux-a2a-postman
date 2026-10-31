# AGENTS.md

Project-specific instructions for tmux-a2a-postman.

## 1. Development Checks

After implementation work:

- Stage new files with `git add <new-files>`; Nix flakes only see
  Git-tracked files.
- Run `nix flake check` and `nix build`. Both must pass before switching
  sessions or creating new tasks.
- Check README.md and skills/*/SKILL.md for removed commands, renamed flags,
  deleted packages, and links to deleted documents.

When changing Go dependencies, go.mod, go.sum, Go versions, or vendorHash:

- Run `go mod tidy`.
- Run `nix build --option substitute false --print-build-logs`.
- If Nix reports a vendorHash mismatch, copy the reported `got:` hash into
  flake.nix and rerun the build.

## 2. Runtime Portability

Do not use /proc/{pid} for process liveness: it fails on macOS. Use
`os.FindProcess` and `proc.Signal(syscall.Signal(0))`; nil and EPERM mean
alive, ESRCH means dead. Process liveness alone does not establish tmux
server incarnation, session ownership, or workspace identity.

Runtime tests must use an isolated tmux server and separate temporary state
and configuration. Track and clean up only fixtures created by that test.
Never kill unrelated sessions or processes. See CONTRIBUTING.md for checks.

## 3. Release Hygiene

- The tag-push workflow owns GitHub Release creation through GoReleaser.
- Do not run `gh skill publish --tag` inside that workflow: it tries to create
  the tag and release itself.
- Use `nix run '.#skill-check'` to validate skills/*/SKILL.md.
- Keep one release owner per flow: manual skill publishing uses
  `gh skill publish --tag ...`; repository releases use a pushed v* tag and
  GoReleaser.
- If a tag workflow fails because the workflow at that tag is broken, fix
  main and create a new tag. Rerunning the old tag uses its old definition.
