# Issue #677 dev-shell validation

- Change: add `pre-commit` to `commonDevPackages` in `flake.nix` so the existing
  `.envrc`/direnv flake shell exposes the policy tool.
- Commit: `8b15709c4738366ed7cf5b378a2c1b8b8401efb3`.
- Validation: `direnv exec . pre-commit --version` reports 4.5.1;
  `pre-commit run --all-files` executes the full configured hook set. Existing
  repository defects remain: skill metadata rejects
  `skills/postman-send-message/SKILL.md` because its first line is not `---`;
  treefmt hit a transient cache-database-busy timeout. `nix flake check` passes.
- Push: blocked by local policy hook (`pushing is denied`); branch is
  `issue-677-two-phase-replacement`.
