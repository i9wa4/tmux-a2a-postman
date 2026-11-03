# Orchestration — from a request to an evidenced result — recovery

## 1. Interruption, failed delivery and handoff

Use `skills postman-records` on start/resume. Reconcile original request,
follow-ups, read/sent evidence, unfinished replies, review stage and next action
against the same canonical index. Unknown command outcome never authorizes
replay. Late replies retain their original correlation and cannot close a fresh
request.

A send may have durable mail but an unavailable/uncertain notification. Inspect
its actual returned ID/outcome before deciding the next action; do not resend
an unknown attempt. If the route failed, report the concrete native/permission
failure and retained mail/evidence. Use an already authorized available return
route or explicit handoff to the owning coordinator/human; if none exists,
record BLOCKED with the exact manual recovery needed (for example opening the
intended console and POP). Do not wait circularly for instructions through the
same broken path or grant yourself tmux/session repair authority. Never bypass
by reading another role's raw inbox or impersonating its POP.

Handoff includes original purpose/checklist, full repository/branch/worktree,
canonical index and linked details, current revision, owners, pending requests,
review/approval lineage, uncertainty and next verifiable action. Recipient
checks scope/provenance and reads relevant records before continuing.
