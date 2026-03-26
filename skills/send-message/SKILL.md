---
name: send-message
description: |
  Send a message to another node using tmux-a2a-postman send-message.
  Supports shorthand (k=v,k=v) and JSON input via --params flag.
  Use when:
  - Sending a message to another agent node in the current tmux session
  - Using --params to supply to/body/idempotency-key without individual flags
  - Discovering command options via schema before constructing a call
  Do NOT use for daemon management (start/stop) or reading inbox messages.
---

# send-message

Sends a message to a recipient node in the current tmux-a2a-postman session.

## 1. Basic Usage

```text
tmux-a2a-postman send-message --to <node> --body "message text"
```

## 2. --params Flag

`--params` accepts a flat shorthand or JSON object to supply command options.
Only flags in the `--params` scope can be set this way. Use
`schema send-message` to discover the exact scope.

### 2.1. Shorthand form (k=v,k=v)

```text
tmux-a2a-postman send-message --params 'to=worker,body=hello'
```

Limitation: values containing commas require JSON form (shorthand splits on ALL
commas).

### 2.2. JSON form

```text
tmux-a2a-postman send-message --params '{"to":"worker","body":"hello"}'
```

### 2.3. Precedence

Explicit CLI flags override `--params` values. To override a param:

```text
tmux-a2a-postman send-message --params 'to=worker,body=hello' --body override
# sends body="override", to="worker"
```

### 2.4. --params scope for send-message

Run `tmux-a2a-postman schema send-message` to get the current schema. The scope
includes: `to`, `body`, `idempotency-key`, `json`. Always-excluded flags
(`context-id`, `session`, `config`, `from`, `bindings`, `send`, `file`) cannot
be set via `--params` and return an error if attempted.

NOTE: `--params` JSON keys use hyphen form matching flag names (e.g.,
`"idempotency-key"`). JSON output keys use underscore form (e.g.,
`"context_id"`). These are asymmetric.

## 3. Schema Discovery

To discover any command's options and required fields at point of use:

```text
tmux-a2a-postman schema send-message   # required: ["to","body"]
tmux-a2a-postman schema next           # output shape
tmux-a2a-postman schema               # postman.toml config schema
```

Do NOT hardcode JSON output shapes or flag lists in role templates when
`tmux-a2a-postman schema <command>` provides them on demand.

## 4. JSON Output

```text
tmux-a2a-postman send-message --to worker --body "hello" --json
# {"sent":"20240101-120000-xxxx-from-worker.md"}
```
