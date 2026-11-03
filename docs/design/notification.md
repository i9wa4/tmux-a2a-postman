# Recipient notification

## 1. Generic terminal input

After durable mail commits, the foreground sender verifies its sent copy and
recipient binding, then uses tmux text paste and one real `C-m` in the existing
agent console. The greeting, `You've got mail:` filename hint and HTML protocol
sentinels remain. The hint asks for argument-free POP and a complete archive
read; the actual POP result selects the message. No mail body or executable
source is pasted. No model API, client registration, shell hook, model name or
hardcoded prompt glyph selects behavior.

A unique tmux buffer is deleted after consumption. A private destination file
lock serializes all Postman processes targeting that recipient, including
cross-workspace senders. No mailbox lock spans input or screen observation.
Native server/session/pane/process evidence is checked around captures and
before input. Ordinary shell prompts and tmux copy/input modes are rejected.

## 2. Settlement, submit and uncertainty

The sender learns the current cursor prefix and screen geometry. It pastes once,
observes the complete hint at the cursor, and waits for 180 milliseconds of
settled input. Then it sends one `C-m`. Post-submit observations look for the
input prefix returning and the hint leaving the current input area while being
visible elsewhere on screen. Output changes alone do not count as acceptance.
Lock admission has a five-second bound. After acquisition the input phase has
its own two-second budget; a one-second margin covers initial verification
within the eight-second post-commit bound. Waiting on an already opened lock
does not repeatedly probe tmux or rewrite workspace state.
The two-second input budget starts after the durable pending guard, and covers
paste, settling, C-m and post-submit screen evidence. Ownership verification
and guard/receipt persistence share the outer eight-second bound rather than
inheriting an almost-expired input deadline. The recipient lock still covers
the entire sequence; persistence failure remains unknown and never replays.

These are generic terminal observations, not proof of model processing. A screen
cannot always distinguish submission from a newline or editor redraw. When
evidence is insufficient, status is unknown. Do not add a second Enter or repeat
the text. A pending destination input guard is durable before the first paste,
so process exit releasing the OS lock does not admit another paste over an
unresolved draft. A later sender may resolve it from sufficient fresh input
evidence; otherwise it leaves both committed messages unread and reports
unknown.

The source notice record prevents replay of accepted or uncertain attempts for
the same message. A pre-input shell/mode rejection can be attempted later for
that exact committed message; a new send always has its own mail identity.
This is bounded foreground work, without a retry queue or permanent observer.

| Fact             | Evidence                                                      |
| ---------------- | ------------------------------------------------------------- |
| Delivery         | Independent sent and receiver inbox/read copies               |
| Accepted hint    | Bounded input-area observations after one C-m                 |
| Unavailable hint | Binding/transport rejected before input admission             |
| Unknown hint     | Input, submit or persistence remains uncertain                |
| POP claim        | Final read archive and durable receipt                        |
| Full read        | Receiver's complete-file evidence, such as its archive digest |
| Response         | Exact correlated reply                                        |

Normally accepted hints are omitted from compact CLI JSON. Other outcomes are
reported in `notification`. Capture, claim, full reading, response and task
acceptance are separate. Notification failure never undoes mail or grants
approval. Use known unread counts as a POP trigger; do not poll an empty inbox.

## 3. Limits and verification

Postman controls its own writers, without saving/restoring human drafts or
excluding unrelated key senders. The former #901 coordinator roadmap was
withdrawn; these limitations remain. Native pane text does not authenticate
model continuity or its historical author. Session snapshots remain separate
from this target-only input check.

The Go test and `nix run .#notification-smoke` share deterministic terminal
editors with and without bracketed paste. Only actual submitted terminal bytes
trigger receiver-owned POP, full archive reading and replies. Busy work runs
independently of the editor. Twenty cases cover rally, concurrent senders,
busy receivers, both interface directions, approval/rejection, copy-mode
blocking and absorbed Enter. The absorbed-Enter control verifies one submit, no
next paste and durable unread mail. Controllers never supply receiver
POP/replies.

Optional real Codex TUI tests use fixed local model decisions over the same tmux
input path. That test provider belongs only to validation. Actual Claude/model
execution is not inferred from deterministic terminal tests. See
[Contributing](../development/contributing.md) and the
[CLI specification](../../help/cli-spec.md).
