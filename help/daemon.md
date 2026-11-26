# daemon [--config <absolute-postman.md>] [--every <duration>] -- heartbeat

Opt-in foreground scheduler for one command. It asks the currently resolved
`tmux-a2a-postman` CLI for the next heartbeat due time, sleeps, then runs a
fresh CLI subprocess and waits for it to exit. The child reads the current
selected default body. `--every` replaces the schedule query's interval.
The daemon inherits the child's stdout and stderr directly and stops on a
child or scheduling error. Stop it with an interrupt.
Configured and explicit intervals must be at least one second. An elapsed
schedule response is rebased by one interval to avoid a busy loop. A future
due time closer than one second is clamped to one second after the query. The
schedule query response is limited to 4096 bytes.

Each query and run resolves the executable on `PATH` again. An immutable
binary path or a PATH entry that does not change will keep selecting the
old CLI. The daemon does not read configuration or keep mail state.
