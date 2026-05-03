---
name: send
description: |
  Send a message to another node using tmux-a2a-postman send.
  Supports shorthand (k=v,k=v) and JSON input via --params flag.
  Use when:
  - Sending a message to another agent node in the current tmux session
  - Using --params to supply to/body/idempotency-key without individual flags
  Do NOT use for daemon management (start/stop) or reading inbox messages.
---

# send

Sends a message to a recipient node in the current tmux-a2a-postman session.

## 1. Basic Usage

```text
tmux-a2a-postman send --to <node> --body "message text"
```

## 2. --params Flag

`--params` accepts a flat shorthand or JSON object to supply command options.
Only flags in the `--params` scope can be set this way.

### 2.1. Shorthand form (k=v,k=v)

```text
tmux-a2a-postman send --params 'to=worker,body=hello'
```

Limitation: values containing commas require JSON form (shorthand splits on ALL
commas).

### 2.2. JSON form

```text
tmux-a2a-postman send --params '{"to":"worker","body":"hello"}'
```

### 2.3. Precedence

Explicit CLI flags override `--params` values. To override a param:

```text
tmux-a2a-postman send --params 'to=worker,body=hello' --body override
# sends body="override", to="worker"
```

### 2.4. --params scope for send

The public scope includes: `to`, `body`, `idempotency-key`.
Always-excluded flags (`context-id`, `session`, `config`, `from`, `file`)
cannot be set via `--params` and return an error if attempted.

NOTE: `--params` JSON keys use hyphen form matching flag names (e.g.,
`"idempotency-key"`). JSON output keys use underscore form (e.g.,
`"context_id"`). These are asymmetric.

## 3. Output

```text
tmux-a2a-postman send --to worker --body "hello"
# {"sent":"20240101-120000-xxxx-from-worker.md","status":"processed","context_id":"...","session":"...","from":"orchestrator","to":"worker","submit_path":"daemon-submit"}
```

Output is always JSON.

The `sent` field stays as the stable filename token. Check `status`:

- `processed` = the CLI observed the daemon handle the send. For daemon-owned
  sessions this is a daemon-submit response; for direct fallback this is
  the daemon consuming the queued `post/` file
- `queued` = only the direct fallback handoff to `post/` was confirmed before
  the observation window closed

`submit_path=daemon-submit` means the send went through the running daemon's
request/response path. `submit_path=post` means the CLI wrote
the session's `post/` queue directly.
