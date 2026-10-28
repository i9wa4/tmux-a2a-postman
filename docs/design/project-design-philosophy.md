# Project Design Philosophy

tmux-a2a-postman should make agent-to-agent coordination in tmux
reliable without hiding workflow state in a supervisor. Issue #937's
file-first target uses short foreground operations and saved mail as
the source of truth; the cutover remains under review.

## 1. Principles

- Keep messages and reply obligations durable, inspectable, and
  receiver-owned after delivery.
- Prefer one recoverable operation per command. Publish the receiver
  copy and sender history before attempting a bounded notification.
- Preserve exact identity and authorization across workspace restarts,
  independent sessions, and parent/child interface routes.
- Expose uncertainty. Unknown liveness, a stale observation, or an
  ambiguous recipient cue cannot be reported as success.
- Keep status reconstructible from files. Optional screen capture is
  diagnostic and bounded; it cannot infer that work is complete.
- Keep help, README, and skills practical. Each contract has one
  canonical home, and obsolete instructions leave at cutover.

## 2. Scope and restraint

Postman coordinates existing agents; it does not launch them or own
their model sessions. Command approval records and checks a human or
agent decision, but does not enforce an OS sandbox. Do not add a
background queue, mandatory observer, or new state machine to imitate
the old daemon. Add complexity only for a concrete delivery,
ownership, recovery, or authorization failure.

## 3. Shell input

Message-body commands accept quoted heredoc stdin from bash and zsh.
Do not treat a non-terminal descriptor's pipe, device, or temporary
file shape as an unsafe body source. Reject explicit body argv and
interactive terminal stdin instead of inferring intent from a
descriptor type.
