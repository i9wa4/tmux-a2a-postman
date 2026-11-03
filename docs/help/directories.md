# Directories — private mail and event-triggered observations

${XDG_STATE_HOME:-$HOME/.local/state}/tmux-a2a-postman/

```sh
locks/<workspace>.lock
<workspace>/workspace.json
<workspace>/runtime/binding.json
<workspace>/runtime/notices/<message-id>.json
<workspace>/runtime/observations/       native session captures and index
<workspace>/agents/<role>/draft/ inbox/ read/ sent/ failed/
<workspace>/agents/<role>/read/<id>.pop.json
<workspace>/agents/<role>/failed/<id>.failure.json
<workspace>/agents/<role>/executions/   audit, decisions and active waiter records
<workspace>/transactions/              interrupted file-operation recovery
```

Workspace keys escape the HOME-relative path, independent of branch/session
name. The real directory, `workspace.json` identity and lock filename retain
this canonical key for compatibility with existing binaries and absolute archive
paths. Directories are private (0700); mail and records are private (0600). An
existing owned application root may remain 0755.

For a key containing `%2D` (a literal component hyphen), normal verified mail
operations publish a readable directory alias, and POP returns paths through
it. Replace only `%2D` with `-`, retain at most 200 prefix bytes, then append
`--` and the first 32 lowercase hex characters of SHA-256 of the **complete
canonical key**. For example, `~/work/team-a` has real key `work-team%2Da` and
alias `work-team-a--<32 hex>`. The suffix distinguishes `team-a` from `team/a`
and truncated prefixes; an unexpected collision fails instead of merging data.
Keys without `%2D` need no alias.

This alias is an owned symlink to exactly the relative canonical basename.
Postman verifies the link and real workspace; internal reads/writes continue
using the real directory with the original no-follow ownership checks. It does
not move or rewrite mail, receipts, transactions, decisions, observations or
logs. Older compatible binaries use the same mailbox and lock. Missing aliases
are published atomically under that lock on the next normal operation; an
interruption requires no migration command. A conflicting file/directory/link
or an alias whose real history is missing is an error, not a fresh mailbox.
Restore the original history before retrying. Read-only getters create no
alias. Enumerating retained workspaces must use real directories once rather
than following aliases and counting the same messages twice.

Saved logs and captures remain until the user deletes them; no automatic
expiry/rotation. Temporary publication files and completed recovery intents are
operational state, not retained logs. get-status never initializes or deletes
anything.

Historical runtime/notice-locks and runtime/input-guards are ignored and left
untouched. Current notifications use per-message attempts without a destination
lock or persisted draft guard.

An optional external_mailbox_root is separate from internal Postman state:

```sh
inbox/ outbox/ read/ binding.json .lock
```

Incoming filenames and literal bodies remain in external read after durable mail
acceptance. Reports are atomic outbox projections of retained sent mail. Native
restart does not reinitialize this binding. See help channel-sync for its
trigger.
