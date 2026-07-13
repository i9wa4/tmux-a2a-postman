# Evidence Replay Contract

Evidence replay is a verification contract, not a message-authored execution
escape hatch. Recorded evidence commands are data until a trusted operator or
configured replay path decides to run them.

## 1. Replay Shape

The replay contract records:

- `cwd`: working directory for replay.
- `env_allowlist`: environment variable names that may be inherited.
- `timeout`: positive timeout for replay.
- `side_effect_class`: one of `read-only`, `idempotent`, or `mutating`.
- `artifact_path`: caller-supplied artifact path, contained under an explicit
  artifact root.
- `expected_artifact_hash`: `sha256:<hex>` hash used to verify the artifact.

Only `read-only` and `idempotent` contracts are eligible for automatic replay.
`mutating` contracts require explicit human confirmation before execution.

## 2. Artifact Containment

Caller-supplied artifact paths are resolved under a trusted artifact root.
Path traversal and symlink escapes are rejected before any hash check. The hash
check verifies artifact content only after containment passes.

## 3. Presence Gate

The evidence presence gate dead-letters completion claims that lack evidence
fields with reason `missing-evidence`, but it ships disabled by default:

```toml
[postman]
evidence_presence_gate_enabled = false
# evidence_presence_gate_after = "2026-01-01T00:00:00Z"
```

Activation must be data-driven. Use the D4 convention meter, or an interim
archive-count proxy, to confirm that reviewers reliably stamp evidence before
turning the gate on. Set `evidence_presence_gate_after` to the activation
timestamp; messages before that timestamp are not affected retroactively.
