# decline-execution — send rejection for one claimed execution request

```sh
tmux-a2a-postman decline-execution --to <POP'd-request-id> --reason 'why'
```

Optional: `--config <absolute-path>`. No stdin, positional arguments, boolean
decision flags or command copy. Read the complete archived request first.
The same ownership, exact-correlation and one-decision rules as
approve-execution apply. A successful rejection delivery has success=true;
the waiting requester reports executed=false with the approver's reason.

State the exact missing authorization, provenance, context or role/scope
mismatch and the intended owner or question. The requester routes it through
orchestrator and, after a matching answer or corrected scope returns, submits a
new typed request. A declined ID remains declined; unchanged rejection is not
retried. See `help approve-execution` for the common decision procedure.
