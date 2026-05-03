---
name: send
description: |
  Send a message to another node using tmux-a2a-postman send.
  Use when:
  - Sending a message to another agent node in the current tmux session
  Do NOT use for daemon management (start/stop) or reading inbox messages.
---

# send

Sends a message to a recipient node in the current tmux-a2a-postman session.

## 1. Basic Usage

```text
tmux-a2a-postman send --to <node> --body "message text"
```

## 2. Public Flags

The public scope includes: `to`, `body`.

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
