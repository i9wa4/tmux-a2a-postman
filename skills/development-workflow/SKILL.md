---
name: development-workflow
license: MIT
description: |
  USE FOR: Planning, implementing or verifying software changes, or
  assessing and reporting their completion against agreed criteria.
  DO NOT USE FOR: Syntax questions or mechanical edits with an obvious check.
---

# Development workflow

## 1. Principles

Start with the original goal, accepted changes, constraints and criteria.
Inspect relevant code and checks. Choose the smallest coherent change that
resolves the next important uncertainty. Make its proof runnable.

Define completion and selection criteria before delegation. Give each child the
original requirements, exact target, bounded scope and evidence. Keep writes
separate; verify integration and leave missing reports explicit. Choose
parallel work when it improves coverage, compares alternatives,
synthesizes independent evidence or refutes a hypothesis. Ask children to
report risks and uncertainties without inventing gates, requirements or review
rounds.

For running behavior, launch the correct build to readiness. Verify
prerequisites and instance identity; reproduce user actions, observe state and
side effects, and clean only owned temporary resources. Execute new
instructions before calling them reusable.

Record the revision, inputs, environment, command and outcome needed to judge
evidence. Measure real workloads
inside a sensitive interval with a fixed method and account for noise. A mock
proves its own mechanics; real-agent claims need real-agent observations.

Separate stale instructions, verification-tool gaps and product defects. Fix the
failing layer without weakening expected behavior. Prevent recurrence through
structure, types, focused lint, runtime checks or behavioral tests where useful;
verify that prevention catches the mistake.

## 2. Match the proof to the work

| Work                  | Proof to seek                                                                                                    |
| --------------------- | ---------------------------------------------------------------------------------------------------------------- |
| Feature               | User path, boundaries and acceptance behavior.                                                                   |
| Bug                   | Reproduction, evidence separating causes, removal of disproven fixes and the same reproduction after repair.     |
| Refactor or migration | Established old behavior, affected callers/config/docs and removal of obsolete paths.                            |
| Performance           | Representative baseline, fixed workload and method, one hypothesis, keep or revert decision and stopping target. |
| Prototype             | Decision, stopping condition and smallest experiment; disposable until selected and verified.                    |

## 3. Before Finishing

Run the smallest meaningful check, then required gates. Assess the agreed
criteria and report outcomes, evidence and remaining work. Extend checks only
for a concrete risk. Stop when the result is supported and state gaps. Preserve
useful decisions and pending work when needed; a small task needs no large log.
