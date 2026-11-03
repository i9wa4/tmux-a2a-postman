# Directories — private mail and event-triggered observations

${XDG_STATE_HOME:-$HOME/.local/state}/tmux-a2a-postman/

```sh
locks/<workspace>.lock
<workspace>/workspace.json
<workspace>/runtime/binding.json
<workspace>/runtime/notice-locks/<role>.lock
<workspace>/runtime/notices/<message-id>.json
<workspace>/runtime/input-guards/<role>.json
<workspace>/runtime/observations/       native session captures and index
<workspace>/agents/<role>/draft/ inbox/ read/ sent/ failed/
<workspace>/agents/<role>/read/<id>.pop.json
<workspace>/agents/<role>/failed/<id>.failure.json
<workspace>/agents/<role>/executions/   audit, decisions and active waiter records
<workspace>/transactions/              interrupted file-operation recovery
```

Workspace keys escape the HOME-relative path, independent of branch/session
name. Directories are private (0700); mail and records are private (0600). Saved
logs and captures remain until the user deletes them; no automatic
expiry/rotation. Temporary publication files and completed recovery intents are
operational state, not retained logs. get-status never initializes or deletes
anything.

An optional external_mailbox_root is separate from internal Postman state:

```sh
inbox/ outbox/ read/ binding.json .lock
```

Incoming filenames and literal bodies remain in external read after durable mail
acceptance. Reports are atomic outbox projections of retained sent mail. Native
restart does not reinitialize this binding. See help channel-sync for its
trigger.
