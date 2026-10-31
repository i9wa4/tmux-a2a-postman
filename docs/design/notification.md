# Recipient notification

## 1. Supported integration

The normal route is recipient-owned Bash/Readline. Follow the setup in
[README](../../README.md#1-install-and-configure) or `help register-notifier`.
The installed binary emits its own setup with `notifier-script`. Source it
from the receiving pane's interactive Bash after assigning the role title.
The setup selects Emacs Readline, reserves Ctrl-x Ctrl-p, and registers the
exact native role, pane and pane-process incarnation. Registration must be a
direct child of that pane's Bash, not an arbitrary sender or nested shell.

The recipient handler receives one POP-result JSON document on piped stdin. It
reads the complete archive through EOF, performs its work and sends any exact
reply. The supplied Codex handler uses `codex exec --sandbox workspace-write`
for a fresh noninteractive turn; Codex configuration and sandbox policy still
apply. Other executable handlers can implement deterministic roles such as a
local reviewer. Interactive Codex/Claude TUI input is not supported by this
adapter.

## 2. Delivery and notification

After publication the CLI verifies the committed sent copy, destination
workspace ownership and registered target. The transport rechecks the native
binding and that Bash owns the foreground prompt, then queues only Ctrl-x
Ctrl-p. It never sends Enter, a command, a body or a generated shell string.
Readline invokes the installed function while retaining READLINE_LINE and
READLINE_POINT, so a pending command is not submitted. No screen-content
heuristic or check-then-Enter is used.

Same-session and declared direct parent/child interface routes share this
path. The latter uses the verified destination runtime and its registration,
never the sender's same-named local role. Notification runs outside mailbox
locks with a fresh two-second budget after commit.

| Outcome              | Evidence                                                             |
| -------------------- | -------------------------------------------------------------------- |
| Durable delivery     | Independent sent and inbox/read mail files                           |
| `accepted` notice    | tmux accepted the Readline key sequence                              |
| `unavailable` notice | Setup/target could not be verified; no notification attempt          |
| `unknown` notice     | Transport outcome may be ambiguous; never blindly retry              |
| POP claim            | Receiver read archive and `.pop.json` receipt exist                  |
| Full read            | Recipient-specific evidence, such as the smoke handler's body digest |
| Response             | Exactly correlated sender/recipient mail and claim receipt           |

Acceptance does not prove POP, full read, understanding, or a response. A busy
handler may defer queued input until it returns. Missing or stale registration
leaves durable unread mail and an explicit notification failure. New known
unread self-state is an action trigger; idle repeated POP/status loops are not.
Handlers must not read terminal stdin or replace the recipient Bash with an
interactive TUI while retaining its registration. Dedicated recipient panes
are the supported host; a shell restart requires sourcing setup again.

## 3. Verification

`TestNotificationDrivenRoundTrip` uses real tmux, the rebuilt CLI's emitted
setup, and real Bash/Readline. After the initial send only the notification
hook invokes the handler. The handler POPs, reads the full file, records a
SHA256 digest, sends an exact reply, and the return notification drives the
sender claim. The test also preserves an unsubmitted composer command.
`TestNotificationDisabledCannotDriveRoundTrip` removes registration and proves
that durable delivery alone produces no recipient actions.

Parent/child tests run both directions. Approval tests exercise approved and
rejected decisions through this same notification route; the requester wrapper
receives its decision without a requester model POP. No test requires paid model
access. See [the recorded smoke check](../../.task-artifacts/952-smoke.md).
