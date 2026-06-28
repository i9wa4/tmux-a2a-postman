# Command Approvals

`execute-bash` is a wrapper for lightweight command approval choreography. It
records command metadata, asks for approval through a durable approval thread,
and applies the configured mode before running `bash -lc`.

## 1. Policy

Policies live in `postman.toml` under `[[postman.command_approval]]`.

```toml
[[postman.command_approval]]
requester = "worker"
label = "nix-build"
category = "verification"
reviewer = "orchestrator"
mode = "blocking"
approval_ttl_seconds = 900
```

`requester`, `label`, and `category` are match keys. Empty values and `*`
match any value. CLI flags may override `reviewer`, `mode`, and expiry for a
single command.

## 2. Running Commands

```sh
tmux-a2a-postman execute-bash \
  --label nix-build \
  --category verification \
  --reason "verify release build" \
  --command "nix build"
```

The wrapper stores requester, reviewer, label, category, mode, command digest,
reason, expiry, approval thread id, decision, and exit status. Full command
text is omitted by default. Add `--store-command-text` only when the command
body is safe to keep in the local audit log.

## 3. Modes

`advisory` records the request and audit metadata, warns when approval is not
present, and continues.

`warn-only` records the request and refuses execution unless
`--override-approval` is supplied. The override is recorded in the audit event.

`blocking` refuses wrapper-mediated execution unless the matching approval
thread has a non-expired approved decision from the configured reviewer for the
exact command digest. Missing, stale, rejected, expired, wrong-reviewer, and
changed-digest approvals do not run.

## 4. Decisions

When `execute-bash` requests approval, it prints the approval thread id in the
wrapper metadata. A reviewer can decide the thread explicitly:

```sh
tmux-a2a-postman execute-bash \
  --thread-id command-approval-... \
  --reviewer orchestrator \
  --record-decision approved \
  --reason "digest reviewed"
```

Use `--record-decision rejected` to reject a pending command.

## 5. Inspection

Command approval state is inspectable without re-running the command:

```sh
tmux-a2a-postman inspect-command-approvals
```

The output shows each approval thread with requester, reviewer, label,
category, digest, reason, expiry, timestamps, and status.

## 6. Boundary

This is coordination, not enforcement. `execute-bash` does not sandbox bash,
prevent direct shell execution, prevent another process from running the same
command, or enforce OS-level policy. Use it to make agent command review
explicit and auditable inside a Postman session.
