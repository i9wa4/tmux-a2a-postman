# Reviews — independent evidence and accountable convergence — gates

## 1. Admission, Rendering and Bounded Stages

| Stage                                    | Owner                                       | Admissibility or decision                                                                                                                                                                                                                                         | Allowance                                                         |
| ---------------------------------------- | ------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| Reviewer packet admission                | Guardian                                    | Required packet fields, valid source/identity representation, evidence reachable by assigned reviewers, and recorded perspective coverage. Quarantine malformed input and return exact defects to its producer.                                                   | Initial submission plus two corrections per packet lineage.       |
| Guardian final-report structure (Tier 1) | Orchestrator                                | Chosen rendering is valid; required round record and evidence are readable; Guardian decision and next action are explicit; recorded coverage and prior-ID dispositions reconcile. Return structural defects to Guardian, without ordering worker implementation. | Initial submission plus two corrections per final-report lineage. |
| Substantive work review                  | Guardian, synthesizing independent evidence | Material findings against frozen criteria/baseline require worker rework of the frozen batch. Only Guardian decides materiality and convergence.                                                                                                                  | Initial review, rework 1 of 2, rework 2 of 2.                     |

Record each stage's owner, reason, current count and predecessor in the existing
review/task artifact. No additional ledger service is needed. Structural checks
verify the report and its evidence references; they do not authorize
Orchestrator to take over source review. Tier 2 is only an explicitly requested,
bounded meta-review, not a recursive response to a Tier 1 defect.

### 1.1. Stable Lineage and Exhaustion

Guardian binds packet corrections privately to the frozen target, substantive
round, required perspective and packet purpose. Orchestrator binds final-report
corrections to that target, round and authorized report purpose. Keep real
identities and correction linkage in Guardian's private control envelope, never
in a blind packet. Every corrected blind packet still gets a fresh alias.

A new alias, filename, message, producer restart, rewritten report or new code
head for the same batch does not create a new lineage. A substantive rework
advances the work round only when Guardian requires and the worker submits an
actual material-batch correction; it is not a formatting escape. Its newly
required packet/report has a new round-scoped lineage, linked to the prior one.
A genuinely new target needs an explicitly authorized distinct scope/outcome
and a new recorded freeze. Merely renaming or splitting the same unfinished
target does not qualify. Unrelated concurrent tasks have independent counters.

After an initial submission and two structural corrections, another invalid
submission exhausts that structural stage. After rework 2, remaining material
findings exhaust the substantive stage. Stop with `BLOCKED`, name the exhausted
loop, attempted counts, affected IDs/evidence gaps, responsible owner and
smallest next decision. Only the human through the interface may authorize an
extension; record it explicitly rather than resetting a lineage. A genuine
correction within an allowance does not itself consume another work round.

Missing evidence has two possible causes: a missing reference to an already
performed, accessible verification can be corrected structurally; a test never
run or evidence inaccessible to its reviewer remains unverified. Filling a
field with an assertion cannot close it. Route actual material defects through
substantive review, and unavailable proof through `BLOCKED` with the evidence
owner. Neither is a cosmetic success or an automatic risk acceptance.

### 1.2. Prose, Optional JSON and Verdict Authority

Track A is ordinary findings-first prose with a justified caller-facing verdict.
Track B is explicitly chosen structured JSON under the sole shape owner:
the complete installed `tmux-a2a-postman help postman-review report` topic.
Read it independently of task cwd; a missing required topic blocks the affected
action. No dotfiles checkout or installed personal Skill tree is required.
For valid Track B the JSON verdict is authoritative for its report; no second
prose verdict is required. Both tracks supply or reference the readable required
round record, recorded perspective set/coverage, material finding coordinates
(or `no file applicable`), reconciled duplicates and concrete evidence. A valid
JSON object alone does not excuse missing review evidence. Do not copy its
schema into role templates or force JSON on prose consumers.

Reviewer/critic `APPROVED` or JSON `approve` is a recommendation based on its
bounded evidence, never Guardian authority. `NOT APPROVED`/`needs-attention`
flags findings; `BLOCKED` identifies missing authority or evidence. Guardian
reconciles these inputs, records disagreements and alone decides `CONVERGED`,
`REWORK`, or `BLOCKED`/`ESCALATE`. The caller-facing mapping is respectively
`APPROVED:`, `NOT APPROVED:`, or `BLOCKED:`. A structured Guardian final report
uses `approve` for convergence and `needs-attention` otherwise; its linked round
record distinguishes rework from blocking and names the next owner/action.
Orchestrator may reject its structure, but cannot convert a reviewer's opinion
or a format repair into Guardian convergence or worker reimplementation.
