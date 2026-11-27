# channel-sync [--config <absolute-postman.md>]

Bounded foreground intake and catch-up for the explicitly configured external
filesystem binding. Requires its exact native TMUX socket/TMUX_PANE and
workspace/session/interface match.
Reads inbox and read archives, recovers durable mail, atomically publishes
reports to outbox and returns pending exact decision filenames. Uses ordinary
POP and the shared generic tmux notification lock. No daemon, watcher or empty
POP polling.

JSON success/as_of/channel/reason; after verified caller binding it also returns
workspace/self and optional instruction. An unverified caller receives no self
projection. Known own mail and pending external decisions receive their distinct
guidance. accepted identifies durable mail;
notification is terminal input evidence only. published means local
availability, not human receipt. A successful result with more=true instructs
one more bounded foreground scan after handling accepted mail, followed by
reassessment of that result. Partial results remain durable on error; retry the
same identities unchanged. See
`tmux-a2a-postman help external-filesystem-channel` for provisioning and
recovery.
