# Repository development instructions

This file guides work on Postman itself. A user's runtime `postman.md` defines
team routing and cooperation; it is not this repository's development guide.
Follow the
[system-wide design principles](docs/design/project-design-philosophy.md).
Before adding a mechanism, examine total complexity across code, configuration,
instructions, assets and operation; reuse, simplify or remove existing
mechanisms. These development rules are separate from the optional user
notification bootstrap.

Read [docs/development/contributing.md](docs/development/contributing.md) for
setup and complete check procedures. For iterative cloud/tmux investigation,
follow the
[AI development runbook](docs/development/cloud-tmux-debugging.md).

- Stage new files before Nix checks; flakes only see Git-tracked files.
- After implementation, `nix flake check` and `nix build` must both pass before
  switching sessions or creating new tasks. Audit README/Skills for obsolete
  commands, flags, packages and broken links. Dependency/toolchain changes also
  require CONTRIBUTING's tidy and no-substitution build procedure.
- Runtime tests own an isolated tmux server and temporary HOME/config/state.
  Track and clean only their resources; never kill unrelated sessions/processes
  or activate user configuration. CONTRIBUTING owns test lanes and evidence
  limits.
- Runtime process liveness must be portable: no `/proc/{pid}`. Use
  `os.FindProcess` and `Signal(0)`; nil/EPERM mean alive, ESRCH means dead.
  Liveness alone does not establish native incarnation, ownership or workspace.
- Release, tag and Skill publishing require an explicit release request. Normal
  PR work stops at branch/PR publication and validation. Follow
  [docs/development/releasing.md](docs/development/releasing.md), the single
  owner of release/tag procedures; do not create a second publisher or infer
  release intent from passing checks.
