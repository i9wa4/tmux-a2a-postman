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

Start with the goal, accepted changes, constraints and criteria. Inspect code
and checks. Make the smallest coherent change that resolves the next uncertainty
and prove it with a runnable check.

Define completion and selection criteria before delegation. Give each child the
original requirements, exact target, bounded scope and evidence. Keep writes
separate; verify integration and leave missing reports explicit. Parallelize
for coverage, alternatives, independent synthesis or refutation. Children
report risks without inventing gates, requirements or review rounds.

For running behavior, launch the correct build to readiness. Verify
prerequisites and instance identity; reproduce user actions, observe state and
side effects, and clean only owned temporary resources. Execute new
instructions before calling them reusable.

Record revision, inputs, environment, command and outcome. Measure real
workloads consistently within sensitive intervals and account for noise. Mocks
prove mechanics; real-agent claims require real-agent observations.

Separate stale instructions, verification gaps and product defects. Fix the
failing layer without weakening behavior. Prevent recurrence with structure,
types, lint, runtime checks or behavioral tests; verify the prevention works.

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
