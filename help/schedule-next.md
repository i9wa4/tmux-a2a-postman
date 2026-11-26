# schedule-next [--config <absolute-postman.md>] heartbeat

Return a small JSON object with `command`, `every`, and RFC3339Nano
`next_due_at`. The foreground daemon invokes the current CLI afresh for
each schedule query. The interval comes from the selected configuration's
`daemon.defaults.heartbeat.every`; custom configuration is a complete
override. The interval must be at least one second. This query does not send
mail or require a tmux pane.
