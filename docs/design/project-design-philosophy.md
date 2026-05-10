# Project Design Philosophy

tmux-a2a-postman exists to make agent-to-agent coordination in tmux reliable
without turning the session into a hidden workflow system. The product should
stay small enough for an operator to inspect, explain, and recover.

This note applies to all feature work. Issue-specific designs can add local
details, but they should fit these rules.

## Principles

- Keep features simple and robust. Prefer small behavior that always works over
  clever behavior with surprising modes.
- Add protocol or workflow complexity only when it removes a real
  user-facing risk, such as lost messages, ambiguous delivery, or unrecoverable
  state.
- Prefer exact state over heuristic guessing. Record the thing that happened,
  expose the state-machine edge, and let operators see the source of truth.
- Do not silently infer, close, or overwrite important state when the system is
  uncertain. Surface uncertainty as pending, blocked, dead-lettered, or
  otherwise operator-visible.
- Design so mistakes are hard to make. Use explicit commands, clear prompts,
  durable files, and safe defaults before adding policy machinery.
- Prefer small composable primitives over broad workflow engines. Commands
  should do one recoverable operation and combine cleanly.
- Make defaults safe and operator-visible. Default behavior should preserve
  messages, reveal routing or liveness problems, and avoid hidden escalation.
- Keep help, README, and skills concise and practical. They should explain the
  operator loop and durable contracts, not every internal edge case.

## Non-Goals

By default, project design should not add:

- a broad workflow engine
- quorum or fan-out machinery
- automatic overdue, breach, or escalation policy
- heuristic or guesswork closure of messages, requests, or tasks
- docs, help text, or skills that grow for every internal edge case

These can be reconsidered only when they prevent a concrete user-facing failure
that simpler state, clearer commands, or better visibility cannot handle.

## Documentation Discipline

Documentation is part of the product surface. Update README, help, and skills
when an operator contract changes. Keep internal details in focused design notes
or code comments when they only explain implementation choices.

## Applying The Philosophy

For each proposed feature, ask:

- What exact state will the system store or display?
- What user-facing risk does the feature remove?
- What happens when the system is unsure?
- Can the behavior be expressed as a smaller primitive?
- Will the default preserve operator visibility and recovery?
- Does this require changing public help or skills, or only a focused design
  note?

If the answers point to hidden inference, broad policy machinery, or
documentation sprawl, reduce the design before implementing it.
