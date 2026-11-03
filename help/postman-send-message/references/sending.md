# send-heredoc — publish mail and attempt a bounded input notification — sending

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

3. Address a configured node for a new request; every new request requires a
   reply. Address your actual POP'd message ID to reply. The tool recovers its
   exact correlation and authorized return route; never copy another request's
   ID because its text looks similar. The generated archive footer shows the
   exact command. A reply does not open another reply obligation.
4. Combine IDs only when they are all your claimed mail from the same sender
   and return route, and one response actually resolves all of them. Otherwise
   reply separately. Consult installed help for syntax and limits.
