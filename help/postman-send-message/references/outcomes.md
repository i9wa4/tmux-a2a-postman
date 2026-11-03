# send-heredoc — publish mail and attempt a bounded input notification — outcomes

## 1. Read the result

The foreground command publishes an independent receiver inbox copy and sender
sent copy. Read its delivery outcome, notification outcome, and mandatory
post-operation self-state separately. Notification failure does not undo a
committed delivery. A failed or uncertain invocation is not permission for a
blind resend; retain its identifiers and inspect the recorded operation.

The foreground sender pastes the notification into the existing recipient
agent's tmux input and submits it with `C-m`. This transport is shared by all
agent runtimes, including the sender receiving a reply and the command reviewer.
No notifier registration, model-specific API or Postman daemon is needed. A
normally accepted notification is omitted from JSON. A separate `notification`
reports uncertainty or unavailability, never POP or response. Unavailable
targets report `unavailable`; an ambiguous attempt reports `unknown` and is
never replayed. Delivery remains committed in both cases.

Unrelated sends and POP do not close required replies. Use file-derived
inbound/outbound counts to assess remaining obligations; missing state is
unknown. Read the archives when exact historical IDs matter.
