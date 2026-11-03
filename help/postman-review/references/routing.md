# Reviews — independent evidence and accountable convergence — routing

Contract version: `1.1.0`. This reference owns review admission, evidence,
materiality, rendering, stage owners and counters. Apply changes at the next
authorized freeze; do not rewrite frozen in-flight criteria or counters.

## 1. Perspective Set and Routing

Reviewer prompt mapping:

| Perspective  | Prompt reference                              |
| ------------ | --------------------------------------------- |
| Architecture | `<resources>/review/reviewer-architecture.md` |
| Code         | `<resources>/review/reviewer-code.md`         |
| Data         | `<resources>/review/reviewer-data.md`         |
| Historian    | `<resources>/review/reviewer-historian.md`    |
| QA           | `<resources>/review/reviewer-qa.md`           |
| Security     | `<resources>/review/reviewer-security.md`     |

- Guardian defines the required perspective set. Critic never self-selects
  perspectives. If omitted, the set defaults to security, architecture,
  historian, code, and QA; do not substitute fixed five/six/twelve counts
  after narrowing. Run required perspectives in parallel waves.
- Self-review accompanies every wave and never replaces it. Guardian-labeled
  trivials may omit subagents; otherwise no direct-only fallback.
- Every ordinary CODE, SECURITY, QA, HISTORIAN, or ARCHITECTURE reviewer launch
  receives bounded read-only paths, bounded context, and a required output
  shape. The specialized data reviewer remains bounded and read-only.
- A guardian request defines the review scope but cannot override runtime
  delegation permissions. If required launch is unavailable, return
  `BLOCKED: perspective launch not permitted`. Critic sends guardian, not
  orchestrator. Critic reports `Required perspectives`, `Perspectives
  launched`, completed perspectives, and self-review evidence. Validate against
  Guardian's recorded set, not a fixed count. Incomplete coverage is
  `BLOCKED: fewer than required perspectives completed` unless Guardian
  explicitly pre-authorized and recorded a bounded degraded path. Record its
  missing coverage, authority and limitations; it cannot waive frozen outcomes
  or grant risk acceptance. A trivial exception also needs Guardian's recorded
  label and reason; an unexplained omitted perspective is not an exception.
- Use the data reviewer only for specialized questions.
- For every Historian or QA reviewer launch, Guardian or critic explicitly adds
  `tmux-a2a-postman help postman-review lookup` to that reviewer's bounded
  read-only context and names its "Lookup / Read Commands" section in the launch
  input. This complete topic is an authorized reference for lookup syntax; it
  does not grant access to other paths or expand the frozen review target. In
  blind mode, the reference contains no candidate identity, so the blind lookup
  restrictions in section 2 still apply. Have the reviewer confirm it could read
  that section before relying on lookup commands. If it cannot, repair the
  launch input and rerun the reviewer; do not treat a prose pointer or an
  inaccessible file as delivered context.
