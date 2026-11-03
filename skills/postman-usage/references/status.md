# postman-usage — status

## 1. Status and command execution

If configured as the top-level external interface, follow the
`tmux-a2a-postman skills postman-usage external` for external mail
and reports. Ordinary mail still uses its existing exact reply guidance.

`tmux-a2a-postman get-status` reads saved files. It does not claim mail, capture
panes, refresh liveness, or escalate by itself. Use it for a concrete status
request, workflow boundary, or delivery problem; wait passively otherwise. Pane
evidence can be stale or unavailable and is not task state. Eligible sends and
POP (including empty POP) attempt the shared finite whole-server refresh after
mail locks are released; busy refresh skips. `refresh-status` explicitly
collects; both status getters only read its saved server snapshot. Select the
assigned server, session, native pane and workspace under
`sessions[].windows[].panes[]`; its `status` is the node summary and `scope`
records configuration/relations. Do not select by role name alone or treat
operator visibility as management, route or mailbox-read authority. Missing
cache requires an explicit authorized refresh; stale as_of stays stale. These
are screen observations, not authenticated model history. Logs and snapshots
remain until the user deletes them.

Filling an input request closes transport, not task acceptance. After an exact
reply, inspect the returned self-state to confirm the specific obligation was
closed. A task-level completion report needs `Task artifact:`, `Original
checklist: PASS`, evidence, and `Remaining blockers: none`. Use `BLOCKED` with
`Original checklist: FAIL` while requirements remain open. Receivers verify
checklist status, durable references, evidence, and blockers before relaying,
approving, or closing work.

`tmux-a2a-postman execute-heredoc --reason 'why'` with quoted heredoc stdin
coordinates command approval; it is not a sandbox. Consult
`tmux-a2a-postman help execute-heredoc` for the file-first command contract. A
verified present configured approver requires blocking approval; an unconfigured
or verified absent approver permits advisory execution. Probe failure or
ambiguity must not execute. Once blocking starts, rejection, timeout,
interruption, or loss of the approver never downgrades to advisory execution.
Historical decisions do not authorize a new invocation.
If executed is null, inspect the exact audit identified by
recovery_operation_id and the command's own evidence; do not repeat the source
to discover whether it ran.

The approver reads its complete POP'd execution request and chooses one fixed
command, approve-execution or decline-execution, with that request ID and a
reason. Use the actual generated examples; do not transcribe code or invent a
decision JSON. The requesting CLI waits for its exact decision and executes;
the approver does not execute the requested source itself.
