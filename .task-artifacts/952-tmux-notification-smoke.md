# Generic tmux notification verification

## 1. Original behavior

The original notifier at `8520025^` used `set-buffer`, `paste-buffer`, then
`C-m` to send a `Hello, <node>! You've got mail:` hint bounded by
`<!-- message start -->` and `<!-- end of message -->`. The recipient performed
POP, read the archive and replied. This is shared terminal behavior for Codex,
Claude Code and local LLM consoles, not an agent-specific input API.

The replacement restores that path inside the short-lived sender CLI after
mail commits. It retains the original greeting and sentinels, uses a single-line
hint for ordinary line-input consoles, and passes the exact POP ID. A unique
buffer, bracketed paste when supported, 150 ms input-processing delay and C-m
are submitted in one tmux command list. Per-recipient locks prevent text mixing.
No model API, thread ID, registration, shell hook or Postman daemon is required.

## 2. Deterministic terminal matrix

Recorded 2026-10-31 on Linux with real tmux 3.5a. All sixteen cases passed:
plain canonical line input and raw bracketed-paste input, each covering rally,
busy recipient, concurrent independent messages, parent-to-child,
child-to-parent, approval, rejection and blocked notification. The terminal
fixtures need no AI model, account or Codex executable. They only act after
receiving the actual submitted notification bytes. The controller never POPs or
sends replies.

Each rally completes four claims with exact reply-to/input-request correlation,
matching complete-file SHA256 digests and original conversation/context nonces.
Busy sends return before release of the receiver. Concurrent messages are not
mixed or duplicated. Rejected commands do not create execution markers.
Copy-mode blocking leaves durable unread mail with no receiver action.

The main binary at `8bc2780` fails the same initial rally: mail commits with
notice unavailable, remains unread, and neither terminal agent receives input.
Both plain and bracketed negative controls passed.

## 3. Existing Codex through the same terminal input

Real Codex CLI 0.163.0 also passes all eight scenarios, using the vde-layout
`--yolo --no-daemon --no-alt-screen` launch and fixed local model responses.
Only model decisions are replaced; the real TUI receives the pasted hint and
C-m, then runs its own POP/read/reply tools. There is no app-server queue
client.

An initial real-Codex rally returned its send in 0.364 seconds and completed
four claims, starting with `m-ac03c0849c126e4443c5cdc10e58827b`. Conversation
IDs remained `01a192ef-7cca-7bf1-b98e-3e351147791c` (orchestrator) and
`01a192ef-7f49-77a0-b2e8-ebac68a7a676` (worker). Its transcript shows the
ordinary `Hello, worker! You've got mail:` user input followed by recipient tool
work. The model endpoint has no authentication or paid requests.

## 4. Commands and boundaries

`go test ./... -count=1` includes the sixteen real-terminal cases.
`nix run .#notification-smoke -- --case all` reruns them explicitly and emits
machine-readable reports. `nix flake check` runs the generic package tests
without an AI client. Optional Codex checks also passed with Nix-pinned 0.162.1
and installed 0.163.0 on Linux. See [Contributing](../CONTRIBUTING.md) for
individual cases, reports, baseline comparison and optional live models.

Go fault tests cover no replay after uncertainty, duplicate/concurrent attempts,
pre-paste rejection, committed-message verification, mailbox-lock independence
and shell prompt rejection. Runtime tests clean up only their own tmux servers.

As with the original transport, notifications enter the current composer.
Postman does not save/restore human drafts or exclude external input writers.
Acceptance proves tmux input, not POP, understanding or task completion. Actual
Claude model execution is not claimed by deterministic terminal fixtures.
This notification work does not close the broader pane-capture attribution
requirements in #937.
