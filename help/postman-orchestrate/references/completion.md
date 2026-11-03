# Orchestration — from a request to an evidenced result — completion

## 1. Completion and review

Worker DONE means internal artifact readiness. Include `Task artifact:`, each
original criterion and its evidence, `Original checklist: PASS`, changed files,
actual verification and `Remaining blockers: none`. Distinguish observation,
inference, deterministic fixture, real client behavior and model evaluation.
Missing evidence means BLOCKED, not a guessed pass.

Orchestrator routes readiness through guardian and critic. Guardian owns the
final substantive verdict; orchestrator checks the structural report gate in
`help postman-review`. Structural correction is not worker reimplementation.
Messenger relays final DONE only with admissible guardian evidence and original
criteria satisfied. Otherwise return a specific blocker or bounded rework
request. Human authorization, runtime permission, command approval and task
acceptance are separate facts. Existing explicit user authorization persists; do
not ask again for already authorized commits, pushes or PRs. Internal approval
cannot grant missing human authority or waive a runtime restriction.
