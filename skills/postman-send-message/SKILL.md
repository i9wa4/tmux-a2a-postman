---
name: postman-send-message
license: MIT
description: |
  USE FOR: All ordinary Postman sends, including delegated work, exact replies,
  status requests, follow-ups, and terminal reports; preserve literal message
  bodies and handle delivery and notification outcomes.
  DO NOT USE FOR: POP, passive waits, history, or status interpretation (use
  postman-usage); topology changes (use postman-config-auditor).
---

# postman-send-message

## 1. Send

1. Resolve the sender and intended recipient against the current workspace,
   verified binding, configured edges, and return route. A footer is a hint;
   it does not authorize a route or override a role contract.
2. Use quoted heredoc stdin; keep Markdown headings and literal body text.

   ```sh
   tmux-a2a-postman send-heredoc --to worker <<'POSTMAN_BODY'
   Message text, including literal $HOME and `code`.
   POSTMAN_BODY
   ```

3. Use a reply-required policy for work needing a resolving answer. A reply
   must address the exact original message and required-input identifier
   supplied by the current output. Never fill a different request because the
   text or recipient looks similar. Use the installed command's help for
   supported metadata flags.
4. Send terminal or informational reports with no new reply obligation unless
   a response is actually needed. Task completion requires the workflow's
   evidence and acceptance checks; transport closure does not satisfy them.

## 2. Read the result

The foreground command publishes an independent receiver inbox copy and sender
sent copy. Read its delivery outcome, notification outcome, and mandatory
post-operation self-state separately. Notification failure does not undo a
committed delivery. A failed or uncertain invocation is not permission for a
blind resend; retain its identifiers and inspect the recorded operation.

The default notification route requires recipient-owned Bash/Readline setup,
including the sender when waiting for a reply. `notice.status=accepted` proves
only key acceptance; missing/stale setup reports `unavailable`, and uncertain
transport reports `unknown`. Do not select a transport flag or start a daemon.

Unrelated sends and POP do not close required replies. Verify exact fill or
unresolved state in the returned output; missing state is unknown.

## 3. After sending

Wait passively for a genuine notification or exact reply. Do not use repeated
POP, sleep/POP, or status loops as a wait mechanism. Use
[postman-usage](../postman-usage/SKILL.md) and its
[session flow](../postman-usage/references/session-flow.md) for bounded status,
known mail, stale evidence, and escalation.

For cross-session mail use `session:node` only across a declared direct
parent/child interface route with verified native peers. Reply to the original
`from_session:from`; local replies use `from`. Do not infer eligibility from
directory ancestry or matching local node names.
