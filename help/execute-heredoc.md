# execute-heredoc — run exact source in a fresh interactive shell

Usage:

```sh
tmux-a2a-postman execute-heredoc --reason 'why' <<'POSTMAN_COMMAND'
go test ./...
POSTMAN_COMMAND
```

Options: `--reason <text>` (required, 1–4096 bytes), `--config <absolute-path>`.
Source comes from nonterminal stdin, 1–61440 bytes, subject to the escaped
durable-record bound. No --command or positional arguments are accepted.

The shell is tmux's default-shell, with normal interactive/login startup and
first-prompt hooks in the invocation's cwd. Bash and Zsh are supported. The
caller environment is inherited; private shell variables, functions and state
from a different running shell are not copied. Startup must acknowledge within
15 seconds. Command stdin is a private PTY with no input forwarding; stdout
and stderr are separate streams, not TTYs. This is not an interactive terminal
application runner or a security sandbox.

A verified present command_approver_node receives the exact source, reason,
cwd and shell. The CLI waits at most 300 seconds for its exact decision. The
approver POPs and reads the request, then chooses approve-execution or
decline-execution. The requesting CLI executes; the approver does not. Its
active waiter claims only its own decision without an extra TUI notification.
Only an unconfigured or positively absent approver permits advisory execution.
Rejection, interruption, timeout, unknown identity or reviewer loss never
downgrades an already waiting request. Approval wait is not a runtime limit.
Dispatch-audit lock admission waits at most 10 seconds, within the caller's
and original approval deadline, without holding a workspace lock while waiting.

Command stdout and stderr stream unchanged. The final stderr line is JSON:
workspace, as_of, success, executed (true/false/null), exit_code (number/null),
approval (approver, decision, reason; or null), self, and optional instruction
and reason. Instruction concerns known own mail and response obligations; it
does not authorize repeating a possibly started command.
A separating newline is added if child stderr has no final newline. Waiting
progress also goes to stderr. A nonzero command exit is forwarded. An uncertain
possibly started command has executed=null and must not be blindly repeated.
That result includes recovery_operation_id, the exact ID in this workspace's
agents/<node>/executions/<id>.json audit record, for investigation.
Postman saves minimal execution audit, not complete stdout/stderr logs.

All configured roles use this entry for shell commands within their assigned
scope, including authorized read-only checks and role-owned record/report
writes. Postman transport, decision and discovery operations described by their
own help remain direct control operations. Approval does not expand a role's
assignment or bypass human and runtime permission limits.

If a decision needs missing human authority, route the concrete question
through the configured coordinator, immediate parents and human interface.
Preserve the answer's source, target, effect and conditions. A rejected request
remains rejected; submit a new typed request with new grounds after the answer.
Do not blindly repeat an uncertain possibly-started operation.
