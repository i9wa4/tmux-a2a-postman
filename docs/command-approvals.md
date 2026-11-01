# Foreground command approval

The exact public contract is in
[CLI specification](cli-spec.md#7-execute-heredoc). This wrapper coordinates
review; it is not an OS sandbox.

## 1. Request and decision

`execute-heredoc --reason 'why'` reads exact source from quoted heredoc stdin.
The request records source, reason, invocation cwd and tmux's default shell.
A present configured `command_approver_node` receives a typed, reply-required
message through ordinary durable mail and the common terminal notification.

The approver POPs without arguments, reads the complete returned archive, and
chooses `approve-execution` or `decline-execution` with the actual request ID
and an explicit reason. Generated archive guidance shows both alternatives. The
CLI obtains exact source and correlation from the archive and claim receipt. No
hand-written JSON, code copy, boolean or decision literal is accepted.
Duplicate/conflicting choices reserve no second decision, including concurrent
helpers. Decision-delivery success does not assert execution success.

Only an unconfigured or positively absent reviewer permits advisory execution.
Unknown configuration or identity fails without execution.

## 2. Waiting and execution

The requesting foreground CLI waits up to 300 seconds and claims only its own
exact decision from its pinned approver. Other mail stays unread. An active
waiter is proven by a matching private file record plus its held OS lock;
a stale unlocked record does not suppress ordinary notification. An already
claimed decision also needs no extra hint. The blocked requester model does
not need to POP. Rejection, timeout, interruption or reviewer loss never
converts waiting into advisory execution.

After authorization, a fresh interactive/login shell initializes normal
startup files and first-prompt hooks in the approved cwd. A private PTY handles
initialization and command stdin; there is no input forwarding. Separate pipes
stream command stdout and stderr. Exported environment is inherited; a different
live shell's private variables, aliases and functions are not copied. Startup
loads those from normal configuration. Project loaders obey their usual trust
and hook behavior; Postman does not approve an untrusted `.envrc` for the user.

Final JSON is a distinct last stderr line, with an inserted newline if needed.
Human review reason and wrapper failure reason have separate fields. Nonzero
child exits are forwarded. Approval waiting has a deadline; approved command
runtime has no imposed 300-second limit. Cancellation kills the owned shell
process group, including ordinary descendants. Deliberately detached descendants
are not an OS sandbox guarantee.

## 3. Recovery and retention

A fresh operation ID and request correlation identify every invocation.
Pre-dispatch and terminal audits are never a durable scheduler or reusable
grant. An interrupted possibly started execution is unknown and never replayed.
Late, wrong, duplicate or historical decisions cannot authorize a fresh call.
Two intentionally repeated calls may execute twice.

Audit retains exact invocation and minimal decision/outcome facts, without
saving complete stdout/stderr. User-requested redirection can be part of the
approved source. Saved records remain until user deletion; no automatic expiry
or rotation is installed. No daemon policy, command digest, reconciliation,
cross-invocation deduplication or resume engine remains.
