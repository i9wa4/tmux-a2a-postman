# Notification Design

This is the target file-first notification contract for issue #937. The
daemon-free CLI work is still under review; a positive production recipient
cue has not been proven.

## 1. Commit before cue

One foreground `send-heredoc` invocation validates the route and publishes
separate receiver-inbox and sender-sent copies. Only after both copies commit
does it attempt a bounded direct notice. The send result reports delivery and
notice separately. A failed or ambiguous notice never rolls back mail and
never causes another message to be delivered automatically.

The notice tells the recipient to run `pop` for the committed message ID.
It does not carry the sender's body or require the recipient to POP before
the sender returns. `pop` moves the receiver's own message to its final read
path before returning that path. The recipient reads the complete archived
Markdown before acting on the message.

## 2. Recipient input boundary

A pane title, screen capture, or tmux paste followed by Enter cannot prove
that the recipient app accepted a turn. Enter can submit text already present
in the recipient composer. Generic tmux pane-input notification is therefore
disabled.

A supported recipient adapter must own an atomic blank-composer submission
or an enforceable lease across every input writer. It must bind the notice to
the committed message ID and verified recipient server, session, pane, and
process incarnation. It submits once while input is exclusive, then returns
app-owned immediate and settled UI evidence for that same ID and an empty
composer. A pending immediate observation may be resolved by a bounded
settled observation; an unrelated immediate ID cannot.

The post-commit notice has its own finite deadline. Missing capability or a
busy composer is `unavailable`; ambiguous output, timeout, mismatched
receipt, or changed binding is `unknown`. Only a verified same-message
transition can be `sent`. Unknown outcomes cannot authorize another Enter,
notice retry, or mail resend. A disposable supported-recipient UI test must
prove the positive cue before this production path is complete.

## 3. Optional observations

Related-pane capture may provide bounded, timestamped diagnostic evidence.
It cannot change mailbox truth, claim mail, or prove that an agent is working.
Stale, missing, conflicting, or unverified activity evidence is `unknown`.

Auto-compaction detection remains reusable for a future optional observer.
The four core commands require no daemon, startup PING, or background cue
queue. Mail obligations and unread counts come from saved files.
