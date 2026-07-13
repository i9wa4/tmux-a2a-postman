# Issue 620 Evidence Replay Contract

## 1. Task

Implement issue #620: add an evidence replay contract shape, artifact
containment checks, and an evidence presence gate that ships disabled by
default and does not affect pre-activation messages.

## 2. Original Checklist

- [x] Replay contract schema defined: `cwd`, env allowlist, timeout,
  side-effect class, expected-artifact hash.
- [x] Path-traversal and symlink containment implemented for caller-supplied
  evidence artifact paths and verified by tests.
- [x] Auto-replay eligibility is limited to `read-only` and `idempotent`;
  `mutating` requires explicit human confirmation.
- [x] Presence gate with dead-letter reason `missing-evidence` exists and ships
  disabled by default.
- [x] Activation criterion is documented as D4 convention meter or archive-count
  adoption evidence, not a code default.
- [x] Enabling the gate does not retroactively affect messages recorded before
  the activation timestamp.

## 3. Implementation Notes

- Added `internal/evidence` with `ReplayContract`, side-effect class handling,
  artifact path containment, and artifact hash verification.
- Added evidence metadata fields to envelope parsing:
  `evidence_command`, `evidence_artifact`, and `evidence_hash`.
- Added `evidence_presence_gate_enabled` and `evidence_presence_gate_after`.
  The gate is effective only when both are configured and the message timestamp
  is at or after the activation timestamp.
- Delivery policy now has a `missing-evidence` dead-letter decision for active
  gate checks on completion claims missing structured evidence fields.
- Added `docs/design/evidence-replay-contract.md` and linked it from the
  README design references.

## 4. Changed Files

- `README.md`
- `docs/design/evidence-replay-contract.md`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/config/evidence_gate.go`
- `internal/config/evidence_gate_test.go`
- `internal/config/postman.default.toml`
- `internal/envelope/metadata.go`
- `internal/envelope/metadata_test.go`
- `internal/evidence/contract.go`
- `internal/evidence/contract_test.go`
- `internal/message/delivery_policy.go`
- `internal/message/delivery_policy_test.go`
- `internal/message/evidence_gate.go`
- `internal/message/message.go`
- `.task-artifacts/issue-620-evidence-replay-contract.md`

## 5. Evidence Log

- PASS: `go test ./internal/evidence -count=1`
- PASS:
  `go test ./internal/config ./internal/envelope ./internal/message -count=1`
- PASS: `go test ./... -count=1`
- PASS: README and `skills/*/SKILL.md` deprecated-reference scan found no stale
  command, flag, or package references.
- PASS: `git diff --check`
- PASS: `nix flake check`
- PASS: `nix build`

## 6. Remaining Blockers

- None currently.
