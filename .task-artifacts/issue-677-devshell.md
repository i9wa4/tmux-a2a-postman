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

## 1. Terminal checklist

- Implementation: PASS (`8b15709`), with commit-time hooks and `nix flake check`
  passing.
- Publication: BLOCKED. The audited push request is
  `command-approval-6d6aecef3e1ebe86`; approval is absent and the configured
  approver is unresolved. No bypass was attempted.
- CI evidence: historical runs 30165305510 and 29293497938 remain failed; no
  fresh clean run was available without a pushed change.
- Fresh blocking push request: label `push-issue-677-v2`, thread
  `command-approval-33aa8a4740055275`, persisted exact command hash
  `sha256:0fecd9a715ee378ffae8dfd92f36b1fa1347e136e74082dd0b9bad5f84e9c048`.
  It is blocked because `approver` is not discoverable and approval is absent;
  no bypass was attempted.
