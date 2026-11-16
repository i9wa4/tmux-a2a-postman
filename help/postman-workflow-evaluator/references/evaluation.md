# Turn a finding into a bounded improvement

First ask whether the cause can be removed, the path shortened, conflicting
instructions merged, obsolete text deleted, or a reference moved to its
canonical owner. Count total complexity across configuration, code, HelpDocs,
Skills, fixtures and operation. An additional `MUST` is not a fix when the
same rule was already present and ignored. For an enforceable invariant,
consider structured input/output, a source-of-truth pointer, fail-closed
validation, correlation or ownership checks, and a narrow regression. Human
intent and substantive acceptance still require evidence-supported judgment.

For user configuration findings, use the existing `postman-config-auditor`
within authorized scope. Compare deletion, merging, simplification and a
pointer to canonical Help before a minimal essential policy addition. For a
product finding, inspect the current installed contract and code, then assign
a narrow mechanism and test to the developer owner. Do not create an Issue,
PR or code change without separate approval. If no reproducible intervention
is justified, say **not yet solved** and name the smallest safe experiment.

## 1. Candidate versus baseline

Keep the same original objective, checklist and comparable evidence. Record the
first observable divergence, expected outcome and pass/fail evidence. Compare
task outcome, missed requirements, user corrections, detours, time/resource
burden and total code/config/Help/Skill/fixture/operation cost. A saved record
can support only its dated claims. An authored fixture checks classification;
it cannot show actual model behavior. An empirical named-session claim needs
an explicit human request, authorized evidence, actual Skill invocation and
output, review and human acceptance. If faithful replay is unsafe or
unavailable, mark behavioral efficacy UNKNOWN and propose a separately
consented bounded check. No credentials, paid models or live session access
are needed for the authored case below.

## 2. Worked authored case: original checklist survives a correction

This is a **SYNTHETIC, PUBLIC-SAFE** stand-in, not a real human request or an
observed Postman session. Its complete data and expected report are in
[`evals/saved-workflow-case.json`](../evals/saved-workflow-case.json). The
invented human initially asks for a migration guide, checks on macOS and
Linux, and human signoff before publication. A later invented correction says
to keep the Linux requirement and add a rollback note. This amendment adds
item C4; it does not erase C1–C3.

| Time  | Authored evidence                                                                                       | Consequence                                                                        |
| ----- | ------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| T0    | Original checklist C1 guide, C2 both platform checks, C3 human signoff                                  | The objective is fixed before planning.                                            |
| T1    | Plan lists guide and macOS check but omits Linux                                                        | First consequential omission of C2; the reason for omission is UNKNOWN.            |
| T2–T3 | Two format reviews see the same artifact digest and no new issue                                        | One repeated review is evident; elapsed/person-time cost is UNKNOWN.               |
| T4    | Human correction retains C2 and adds rollback note C4                                                   | One user correction and authorized checklist addition, not agent reinterpretation. |
| T5    | Final guide and rollback note exist; macOS check passes; Linux check and human signoff have no evidence | C1/C4 PASS, C2 OPEN, C3 UNKNOWN; task BLOCKED, acceptance UNKNOWN.                 |

The invented baseline report says `PASS` because a formatting command passed,
claims human acceptance, and adds another `MUST` to remember the checklist.
That is an authored **negative control**, not an observed agent failure. The
existing completion rule in `config/postman.md` lines 99–111 already requires
the task artifact, original checklist PASS, evidence, changed files,
verification and no blockers; otherwise report BLOCKED or NOT APPROVED. The
candidate report preserves C2/C3, names T1 as first divergence, records one
correction and one repeated review, rejects the duplicate MUST, and reports
`BLOCKED` with human acceptance `UNKNOWN`. The known fact is omission in the
plan; the mechanism causing the omission is a hypothesis until investigated.
A clearer final status field is a proposed reporting aid, not proof that an
agent will obey it.

From a repository checkout, the deterministic authored-case check is:

```sh
python3 help/postman-workflow-evaluator/evals/check-saved-workflow.py
```

It checks each criterion’s requirement, origin, status and evidence against the
authored request and T0–T5 timeline, plus report record continuity, owner and
efficacy limits. Fourteen independent mutations must fail with specific reasons,
including stale and duplicate records, missing decision branches, changed
requirements, contradictory evidence, wrong owner and unsupported efficacy. Its
`AUTHOR_CASE_CLASSIFICATION_PASS` is a static result only. It does not invoke
this Skill, inspect a live tmux pane, or establish human acceptance. The
separate trigger suite checks only metadata/discovery. A later empirical
comparison, if authorized, must keep the same original task and evidence access
for baseline and candidate and measure actual outcome and total burden.

## 3. Configuration and product decisions

**Config Auditor example (authored):** Three handoff paragraphs contain two
copies of the POP-ID reply rule and one obsolete route. The baseline proposal
appends a fourth reminder. The smaller candidate deletes the obsolete route,
merges the two valid rules into one sentence, and points to canonical Help.
Before/after burden is three paragraphs plus a proposed fourth versus one
sentence and one pointer; two duplicates and one obsolete route are removed.
Check that replies still use the actual POP ID and the intended native route.
The fixture compares baseline and candidate across code, config, Help, Skill,
fixture and operation. The candidate fixture count includes one JSON case,
one checker, one staged trigger suite and three trigger tasks. The checker
compares every authored known inventory value, so contradictory code or config
claims fail even when their prose still says `UNKNOWN`. These are illustrative
counts, not measured time savings; unmeasured maintenance and time costs stay
`UNKNOWN`. The expected
config decision is subtraction of obsolete and duplicate rules before any
policy addition. Add policy only if a
missing user-specific route or permission cannot be expressed by the existing
owner. A config-only user can use Config Auditor without product-code access.

**Hypothetical product defect, not an observed Postman CLI defect:** Suppose a
future CLI version accepted a reply whose claimed request ID did not match its
archived request. First reproduce that behavior against the exact binary and
contract. If confirmed, assign correlation validation and an isolated
regression to product code, not a warning in `postman.md`. If the current CLI
already rejects it, preserve the working invariant and close the hypothesis.
The expected product decision is reproduce-before-assignment, not an inferred
product defect.

If the saved task cannot be replayed safely, the useful result is a bounded
classification and an explicit UNKNOWN for behavioral efficacy. The expected
unsafe-replay branch seeks separate consent for a bounded check. Do not turn the
authored report check, a green command or a transport receipt into a
named-session acceptance claim.
