# Foreground command approval

## 1. Contract

`execute-bash --command '<exact Bash>'` resolves `command_approver_node` from
current Markdown topology. An unconfigured or positively verified absent
reviewer permits advisory execution. Unknown configuration, probe failure, or
ambiguous identity fails closed. This is operational coordination, not a
sandbox or unbypassable security boundary.

A present reviewer receives a fresh reply-required mail through ordinary
durable send and the registered runtime notification adapter. Set up the
reviewer as described in [README](../README.md#1-install-and-configure).
The request body is `{"version":1,"command":"<exact command>"}`. The reviewer
POPs, reads the whole archive and sends ordinary mail addressing the requester,
with the exact `--reply-to` and `--fills-input-request-id` from that request.
Its body is `{"version":1,"command":"<exact command>","decision":"approved"}`
or the same envelope with `rejected`.

## 2. Waiting and execution

Only the foreground wrapper waits, at most 300 seconds. It claims only this
invocation's exact decision from the pinned reviewer, leaving unrelated mail
unread. The blocked requester model never needs to POP. Rejection, timeout,
interruption, changed command/configuration or reviewer loss does not downgrade
to advisory. Notification failure never grants approval or resubmits a request.

The immutable original command runs once after approval. Short workspace locks
protect mail and audit transitions, never the wait or Bash execution. Command
stdout/stderr stream normally; protocol JSON is on stderr. The wrapper forwards
Bash's exit status. Distinct intentional invocations remain independent.

Audit records document request, decision, pre-dispatch and terminal result.
They are not reusable grants, persistent waiters, or a durable scheduler. A
crash after dispatch can leave an unknown outcome; no recovery path repeats
Bash. Late, wrong, duplicate or historical decisions cannot authorize a new
invocation. No old daemon policy, TTL, command digest, thread override,
reconciliation, deduplication or resume mode is retained.
