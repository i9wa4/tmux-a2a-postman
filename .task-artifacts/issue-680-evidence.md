# Issue #680 evidence

- Existing issue verified:
  [#680](https://github.com/i9wa4/tmux-a2a-postman/issues/680).
- Posted reproduction evidence:
  [issue comment](https://github.com/i9wa4/tmux-a2a-postman/issues/680#issuecomment-5230587354).
- Evidence includes the exact PreToolUse denial, absence of wrapper
  execution/thread/hash/expiry at that denial, two expired approval attempts,
  and no remote mutation during denied trials.
- Non-bypass fix stated: persist and audit the reviewed `execute-bash` request
  and create its canonical approval record before execution-policy enforcement,
  preserving unaudited-push denial.

Original checklist: PASS (issue discovery, evidence publication, and non-bypass
fix statement). Remaining blockers: none for this report-only task.
