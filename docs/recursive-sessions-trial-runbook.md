# Parent/child session trial

Create isolated parent and child tmux sessions rooted at different directories
below a temporary HOME. Title their interface panes with configured roles.
Declare both sessions and their direct parent relation in Markdown frontmatter
as shown in the
[format reference](../skills/postman-config-auditor/references/postman-md.md).
Each session's topology declares its own local `interface_node`.

Start an agent console in each interface pane. Send one reply-required message
to `child:interface-role`. The common tmux paste/C-m notification must drive
child POP and full archive reading, its exact reply to `parent:interface-role`,
and the parent claim. Repeat in the other direction.
Check each workspace's independent sent and read files and POP receipts.

Do not invoke receiver actions from the test controller. Do not infer routes
from directory ancestry, scan a fleet of panes or notify a same-named local
role. Missing/ambiguous peer binding must fail explicitly. Clean up only owned
fixtures. `TestNotificationDrivenTerminalAgents` automates both directions
without a model or daemon. Run either direction with
`nix run .#notification-smoke -- --case parent-child` or `--case child-parent`.
