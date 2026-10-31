# Design philosophy

Favor readable files, exact identities, small foreground operations and honest
unknown outcomes. Durable delivery and notification acceptance are separate
facts. Do not infer task success from transport closure or screen changes.

Keep short workspace locks and exact reply/approval correlation. Remove dead
runtime paths and redundant policy matrices. Do not add compatibility wrappers,
background delivery workers or model-side polling to solve recipient activation.
See [architecture](file-first-architecture.md) for the operative contracts.
