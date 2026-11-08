# Parent/child session trial

Create isolated parent and child tmux sessions rooted at different directories
below a temporary HOME. Title their interface panes with configured roles.
Each session's configuration declares its own local `interface_node` as shown
in the [format reference](../../help/postman-md.md). Register each live
interface through a normal configured operation before the first cross-session
send. Canonical directory containment and current native registration establish
the direct parent/child route; no parent frontmatter or tree override is used.

Start an agent console in each interface pane. Send one reply-required message
to `child:interface-role`. The common tmux paste/C-m notification must drive
child POP and full archive reading, its exact reply to `parent:interface-role`,
and the parent claim. Repeat in the other direction.
Check each workspace's independent sent and read files and POP receipts.

Do not invoke receiver actions from the test controller. Do not treat ancestry
alone, an unregistered pane title, or a same-named local role as a verified
route. Missing/ambiguous peer binding must fail explicitly. Clean up only owned
fixtures. `TestNotificationDrivenTerminalAgents` automates both directions
without a model or daemon. Run either direction with
`nix run .#notification-smoke -- --case parent-child` or `--case child-parent`.

## 1. Recursive team fixture

For deeper native routing, run `nix run .#recursive-smoke`. This development
fixture and its `recursive-teams` Nix check live entirely in Postman. It retains
root, four-level restart/stale-response, six-level branching/concurrent,
rejected-execution and missing-child cases. `--case` selects one; `--depth`
bounds the branching test workload, not supported production topology depth.

The fixture starts a private tmux server and direct deterministic consoles.
Only submitted notification input drives recipient actions. It checks original
goals, exact request/return IDs, full archive digests and common/role policy
across depth, denied non-adjacent routes, independent decisions, no false DONE,
and exactly-once leaf execution. Simulated scope artifacts remain test evidence;
the fixture supplies no task store, real Logbook integration or AI reasoning.

An optional consumer policy is read into a private copy:

```sh
nix run .#recursive-smoke -- --case root \
  --postman-md /absolute/postman.md \
  --skill-root /absolute/consumer-skills
```

Use a compatible messenger/orchestrator/worker/approver/guardian/critic graph
without a workspace-tree override. Additional Skill metadata comes
only from explicitly supplied roots; Postman's own Skills remain available.
The original policy and installed directories are never modified. The default
check has no consumer dependency.

There is no installed configuration-generator command. The private helper under
`scripts/validation/recursive_team` assembles only fixture configuration; normal
users author `postman.md` using the published format and configuration Skill.
Reports retain captures, per-hop archives and proof paths after the owned tmux
server is cleaned up. A passing scripted workflow proves those checks, not
human acceptance, real-model judgment or activation of a live fleet.
