# heartbeat [--config <absolute-postman.md>]

Send one reply-required request to the selected workspace's verified
`interface_node`. A manual quoted heredoc supplies the exact UTF-8 body.
With empty stdin, the current CLI reads `daemon.defaults.heartbeat.body`
from the selected configuration. A custom configuration never inherits
the embedded body's value. The command returns the ordinary mail JSON and
propagates missing route, context, and delivery errors. A committed send
remains successful when notification is not accepted; the JSON reports it.

This operation records delivery, not task claim, progress, or completion.
Read the exact POP archive and reply through the normal mail procedure.
