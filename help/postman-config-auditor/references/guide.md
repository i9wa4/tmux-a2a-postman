# postman-config-auditor

## 1. Audit current configuration

Read `tmux-a2a-postman help postman-config-auditor audit` and
`tmux-a2a-postman help postman-md`. Inspect the single absolute
`postman.md` used by the invocation, its Mermaid edges and native role bindings.
Check direct parent/child interface declarations and recipient runtime
registration. No daemon configuration, TOML overlays or migration gate is
involved.

## 2. Apply requested fixes

For a fix request, edit configuration source directly within authorized scope.
For audit-only work, report findings with concrete patches. Do not edit runtime
mailboxes, broaden routes or treat missing evidence as verified absence.
Use installed `tmux-a2a-postman help` for actual commands and flags.

When a concrete session finding motivates a config change, check whether an
existing rule was present but ignored. A synonymous new rule does not address
that failure. Compare deleting obsolete text, merging conflicting instructions,
shortening the path, and pointing to canonical Help before adding essential
policy. Explain the retained behavior and total instruction burden. If the
cause requires a CLI or runtime invariant, report that owner rather than
loading another workaround into `postman.md`.

For POP/full reading, replies and passive waits use
`tmux-a2a-postman help postman-usage mailbox`; for ordinary sends use
`tmux-a2a-postman help send-heredoc`.
