# Mail Facts and Node State

This is the target file-first state contract for issue #937. The daemon-free
CLI work is still under review; legacy status surfaces remain until cutover.
Saved mail and exact reply records are authoritative. A screen observation
may add diagnostics, but it cannot change those facts.

## 1. Mail identities

A message ID names one immutable exchange and its receiver-owned inbox or
read copy. The sender retains a separate sent copy. A thread ID, when
present, groups related exchanges; it does not identify a mailbox claim.
A reply-required send opens an exact input-request obligation for its
recipient. A reply must name and satisfy that request, with the authorized
sender and recipient relationship preserved. Ordinary mail, an arbitrary
reply, a POP, and a status read cannot close it.

A request's causal link is correlation data, not a return route. Send-time
validation must preserve the original sender, recipient, request, and
authorized parent/child interface route. Two separate relays remain
separate exchanges even if they share an upstream cause. An invalid or
cyclic cause cannot silently redirect a reply. Cause walks are bounded,
track visited message IDs, and report unresolved or truncated chains
rather than presenting a partial chain as complete.

## 2. Receiver-owned transitions

`send-heredoc` commits the receiver's inbox copy and the sender's sent
copy. An interrupted multi-file send uses its operation record to finish
or recover the same message ID; retry does not redeliver an already present
inbox or read copy. A failed delivery retains recoverable draft or failed
content with a reason.

`pop` exclusively claims one eligible inbox message, moves it to that
receiver's final read path, then records and returns the claim. A second
claimant cannot receive the same message. Recovery covers interruption
after the move, after the receipt, and during output. Empty POP still
reports the caller's saved-file state.

## 3. Self-state and status

Every send and POP returns known remaining unread mail and exact open
reply obligations for its caller. Inbox attention and required response
are separate: unread no-reply mail needs reading but opens no reply slot.
A sent reply-required message may leave the sender waiting; it does not
make the recipient's other mail disappear.

`get-status` reconstructs these facts from saved files without claiming
mail, registering a workspace, or repairing state. It may include bounded,
incarnation-tagged observations of related panes. Missing, stale, or
conflicting activity evidence is `unknown`, never a claim that the agent
is working. A live pane or changing screen does not override open mail
or exact reply obligations.

A parent may summarize only explicitly authorized child workspaces to a
fixed depth. Each child read reports fetched, unavailable, or stale; a
missing child is never healthy completion. The summary composes each
child's saved facts without inventing a second acceptance state machine.
A decision-needed report remains separate and cites the exact durable
item that needs an operator choice.

## 4. Cutover boundary

The target has no central daemon status machine, background post queue, or
mandatory TUI. Old and new writers must not share active state. An explicit
migration must map old records to a verified workspace and preserve
historical mail; old approval records never become new authorization.
