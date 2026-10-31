# Recipient notification

## 1. Existing Codex conversations

Each recipient runs `register-notifier --runtime codex` from its existing
Codex tool context. Follow [README](../../README.md#1-install-and-configure).
The registration binds the verified native server/session/pane/PID and role
to Codex's own UUID, private CODEX_HOME, executable, topology path and original
workspace root. A metadata-only `thread/read` verifies the UUID and cwd.
Register again after changing conversations or restarting. Codex CLI 0.162.1
and 0.163.0 are tested; other runtime versions must expose the same queue API.

After durable mail publication, a short-lived `codex app-server --stdio`
initializes the native protocol, verifies thread metadata, and sends
`thread/queue/add` with the original UUID. The notification contains the exact
mail ID and POP instruction, never the mail body. It uses a mail-derived UUID
as `clientUserMessageId` and validates the correlated queue receipt. The helper
exits after the request; Postman owns no resident process or delivery worker.
It sends no `thread/start`, `thread/resume`, or `turn/start` request, and no
keystrokes to Codex. The existing recipient runtime consumes its native queue.

This works with `--no-daemon --no-alt-screen`. Codex's external SQLite queue
watcher currently checks at ten-second intervals; busy conversations defer
execution until idle. Acceptance proves queue insertion, not that a particular
model responded. A closed or switched-away conversation cannot execute mail
until it is active again, so registration must track conversation changes.
Codex's sandbox and approval policy still govern recipient tool calls.

## 2. Commit, ambiguity and evidence

The sender verifies its committed sent copy and destination registration, then
rechecks the exact native target binding. Same-session and declared direct
parent/child interface routes use the destination's own registration. No
mailbox lock spans a runtime request. A fresh two-second budget bounds the
post-commit notification independently of caller cancellation.

Before queue insertion, the source writes a private `runtime/notices/<id>.json`
record with status `unknown` and the recipient registration. After a matching
runtime receipt it marks the record `accepted`. Concurrent/repeated attempts
for that same committed message never enqueue again: accepted attempts return
the saved receipt; ambiguous attempts remain unknown. Failure before reservation
is safe to retry for that exact committed message. A notice failure never
redelivers or rolls back mail. These receipts are notification evidence, not a
background retry queue or a competing source of mailbox truth.

| Outcome              | Evidence                                                               |
| -------------------- | ---------------------------------------------------------------------- |
| Durable delivery     | Independent sent and inbox/read mail files                             |
| `accepted` notice    | Correlated native Codex queue receipt, or optional Bash key acceptance |
| `unavailable` notice | Setup/identity/protocol failed before enqueue could be attempted       |
| `unknown` notice     | Enqueue or receipt persistence may be ambiguous; never blindly replay  |
| POP claim            | Receiver read archive and `.pop.json` receipt                          |
| Full read            | Recipient evidence, such as a complete-file SHA256 digest              |
| Response             | Exactly correlated mail and claim receipt                              |

Missing or stale registration leaves durable unread mail. A real notification
or known unread self-state triggers POP; idle POP/status polling is unnecessary.
An accepted queue item does not prove POP, understanding or completion.

## 3. Optional Bash automation

For deterministic shell recipients, source `notifier-script` from the pane's
interactive Bash after setting POSTMAN_BIN, POSTMAN_CONFIG and executable
POSTMAN_HANDLER. Materialize it in a private temporary file before sourcing;
this also works without `/dev/fd`. Nix requires `bashInteractive` for Readline.
The setup selects Emacs Readline, binds Ctrl-x Ctrl-p and registers explicitly
with `--runtime bash`. Registration must be a direct child of the pane's Bash.

The transport verifies the native binding and foreground Bash, then sends only
Ctrl-x Ctrl-p. It sends no Enter, command or body. Readline retains pending
input and invokes one POP; the handler receives its JSON on piped stdin, reads
the archive through EOF, then handles/replies. It must never read terminal
stdin. The supplied `receipt-handler.py` reports a full-read digest and exact
reply, never task completion. Re-source after shell restart. This adapter does
not activate an interactive AI TUI; Codex uses the native route above.

## 4. Verification

`nix run .#codex-notification-smoke -- --case all` launches real Codex binaries
in private tmux panes matching vde-layout's no-daemon/no-alt-screen launch.
Only model decisions are replaced with fixed local Responses API tool calls.
The controller performs the initial send or execute request; only Codex's
recipient tool calls POP, fully reads mail, and replies. Conversation UUIDs,
initial context nonces, archive hashes, exact reply IDs, and unsubmitted drafts
are checked. Cases cover rally, busy, concurrent arrivals, parent/child both
directions, approved/rejected commands, and disabled registration. The negative
control waits longer than Codex's native queue interval and requires durable
unread mail with no recipient action. `nix flake check` runs this suite.

Go process-level fault tests additionally cover wrong thread/cwd, disconnect,
timeout, mismatched receipts, duplicate/concurrent attempts, helper cleanup and
mailbox lock independence. The existing real Bash/Readline process tests remain
for the optional adapter. No default test requires credentials or paid models.
