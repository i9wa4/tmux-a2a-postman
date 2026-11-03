# approve-execution — send approval for one claimed execution request

```sh
tmux-a2a-postman approve-execution --to <POP'd-request-id> --reason 'why'
```

Optional: `--config <absolute-path>`. No stdin, positional arguments, boolean
decision flags or command copy. Read the complete archived request first.
Only the configured approver can decide its own POP'd execution request.
Exact source and correlation come from that archive. A duplicate/conflicting
decision fails before publication. Returns delivery success and common self
JSON; delivery does not imply that the requester executed successfully.
