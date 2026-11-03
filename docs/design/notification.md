# Recipient notification

## 1. Generic terminal input

After durable mail commits, the foreground sender verifies its sent copy and
recipient binding, then pastes a short hint into the existing agent console.
The greeting, `You've got mail:` filename hint and HTML sentinels remain. The
hint asks for argument-free POP and a complete archive read; the actual POP
result selects the mail. No body, executable source, model API, client
registration or shell hook is involved.

Unique tmux buffers are deleted after consumption. Native server/session/pane
and process evidence is checked around captures and before every key. Ordinary
shell prompts and tmux copy/input modes are rejected. Mailbox locks protect
short file transactions; none spans terminal input or observation.

## 2. Screen differences and bounded retries

Closely spaced Enter presses can become literal newlines. The sender separates
paste and Enter, then compares the complete pane text before and after each
Enter. A small insertion/deletion diff can represent a newline or a viewport
scroll; comparing rows by position would mistake scrolling for a large redraw.
Small differences permit another spaced Enter. Substantial change stops extra
keys. Both the attempt count and elapsed input time are bounded. The exact
interval, threshold and limits belong to the embedded
[send-heredoc HelpDoc](../help/send-heredoc.md).

The algorithm never locates an input area, learns cursor prefixes or branches
on Codex/Claude prompt conventions. Equal-looking screens may have different
internal meanings, and background output may create a large difference. The
diff is deliberately only a retry heuristic. Extra Enter can be harmless even
when the first was accepted; it must never become an unbounded key stream or
proof of receiving, understanding, approving or completing mail.

Independent senders do not wait on a destination notification lock. Complete
hints may collect before the last process submits them. There is no shared
last-sender registry or queue. A persistent ambiguous-input guard previously
prevented all later hints even after the original mail was claimed; current
transport neither reads nor updates it. Historical guard/lock files remain
untouched. Every message still reserves a durable attempt before its one paste,
so concurrent attempts for that same ID cannot replay input. Unknown mail is
not re-sent, and a fresh message is not blocked by another ID's uncertainty.

| Fact             | Evidence                                                       |
| ---------------- | -------------------------------------------------------------- |
| Delivery         | Independent sender sent and receiver inbox/read copies         |
| Accepted hint    | Compatibility label for substantial post-Enter whole-pane diff |
| Unavailable hint | Binding/transport rejected before input admission              |
| Unknown hint     | Retry limit, observation, input or persistence uncertainty     |
| POP claim        | Final read archive and durable claim receipt                   |
| Full read        | Receiver's complete-file evidence, such as its archive digest  |
| Response         | Exact correlated reply                                         |

Normally accepted hints are omitted from compact JSON; the label is not
recipient acceptance. Other outcomes appear in `notification`. Failure never
undoes mail or grants execution permission. Known positive unread counts can
drive a finite POP batch; an empty inbox never becomes a polling loop.

## 3. Limits and verification

Postman does not exclude human drafts or unrelated terminal writers, and native
pane text cannot authenticate a model or conversation history. Session snapshots
remain separate from target-only submission observations. There is no daemon,
retry worker or continuous production recorder.

Real isolated tmux tests own terminal editors with and without bracketed paste.
They cover complete simultaneous hints, busy output, newline-only retries,
finite permanently absorbed Enter, both interface directions, eight approval
requests and external channels. Receiver-owned POP, complete archive digests,
exact replies/decisions and exactly-once execution effects are mandatory; a
controller never supplies those receiver actions.

Optional real Codex tests replace model decisions with a local deterministic
Responses fixture, exercising the actual TUI and recipient tools without
account authentication. These tests do not prove live Claude or model behavior.
[Contributing](../../CONTRIBUTING.md) owns the matrix and commands; the
[CLI specification](../cli-spec.md) owns the observable result contract.
