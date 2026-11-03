# Commands

launch-agents <preset>: optional one-shot dedicated team creation.

```sh
--list and --dry-run are inert; see help launch-agents.
```

channel-sync: bounded external inbox intake and report/decision catch-up.
channel-report-heredoc --to <external-POP'd-id> --state <done|blocked|pending>:

```sh
heredoc stdin; pending additionally requires --scope <action-context-SHA256>.
```

refresh-status: finite whole-server collection; busy skips immediately.
get-status: cached server JSON; no command-specific options or positional
arguments. get-status-oneline: read-only operator overview; no command-specific
arguments. pop: no command-specific options or positional arguments.
send-heredoc --to <node|session:interface|POP'd-id[,POP'd-id...]>: heredoc stdin.
execute-heredoc --reason <why>: heredoc stdin, child streams and final JSON on
stderr. approve-execution --to <one-POP'd-execution-request-id> --reason <why>.
decline-execution --to <one-POP'd-execution-request-id> --reason <why>.

Common: --config <absolute-postman.md>, --help. Duplicate options fail.
Status commands accept --config for compatibility only; they never read it.
help [topic] and version are information-only. See help <command> for results.
