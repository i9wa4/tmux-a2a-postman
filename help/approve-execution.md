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

## 1. Decision procedure

Match the claimed request ID, exact source, reason, cwd, shell and verified
requester identity to the current team configuration. Check the requester's
configured role and assigned scope, including effects of nested commands,
redirections and writes. Authorized read-only checks may belong to any role;
implementation, builds and tests normally belong to workers. Preserve assigned
record, report and Postman operations for coordinators and reviewers without
turning those duties into general task-execution authority. A mismatch returns
the reason and intended owner to the requester for orchestration.

Apply human permission supplied with the request or the current shared approval
record only within its stated target, effect, time and conditions. A faithful
relay preserves permission already given by the human within that scope; a
message route, role label or bare forwarded claim grants none. Valid permission
need not be entered in the shared record or restated in the approver pane. If
provenance or scope is missing, decline with the concrete question for the
requester to route through orchestrator. Historical conversation recovery also
goes through that route. Do not execute or investigate the task in this role.

One typed decision resolves this request ID only. Approval is neither a reusable
grant for a later invocation nor a substitute for human publication permission,
runtime permissions or substantive artifact review. Only the requester executes.
