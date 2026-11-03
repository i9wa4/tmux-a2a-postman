# Reviews — independent evidence and accountable convergence — ledger

## 1. Ledger, Batch, and Report

- A review is governed by the contract version in effect when Guardian
  freezes scope and criteria; Guardian records that version in the frozen
  scope alongside the targets. A contract change landing mid-review does
  not retroactively alter the criteria of a review already frozen -- it
  applies to the next freeze.
- Freeze the baseline before the initial review: the base commit and relevant
  pre-change behavior against which candidate regressions are judged. The
  task owner may declare `mitigation` mode before this freeze for a time-boxed
  live-problem fix; otherwise use `standard` mode. Guardian records the mode,
  owner, reason, and baseline. A later mode change is a criterion change.
- Materiality is the effect and gate decision, separate from packet severity:
  `HIGH` covers loss or corruption and common-path regressions against the
  baseline; `MEDIUM` covers an unmet frozen outcome; `LOW` covers test-quality
  items and rare edges of the original problem that do not cause loss or
  corruption. Apply HIGH first; an otherwise rare edge that violates a frozen
  outcome is MEDIUM rather than LOW. In standard mode, HIGH and MEDIUM gate;
  LOW is non-gating. In declared mitigation mode, only loss/corruption or
  regressions against the
  baseline gate; other findings become named follow-ups. Apply the same rule
  to comparable failures in sibling paths, or record the factual difference
  that changes their classification.
- Every round explicitly compares candidate head with the frozen baseline for
  common-path latency and liveness, naming the observation or verifier. A
  prescribed design does not excuse a material regression.
- Every stable entry has severity, materiality, `OPEN`/`CLOSED`, closure
  disposition (`fixed`, `accepted`, `deferred`, or `rejected`), rationale, exact
  path/evidence coordinates, closure condition/evidence, and accepted-risk
  authority/owner. `superseded` is a reconciliation relation naming
  successor/reason.
- Finding IDs are never reused. Neither an open nor a closed ID may identify a
  distinct finding later. Give a changed criterion or distinct root cause a
  fresh ID; link the old and new entries with `supersedes` (or an equivalent
  relation) and reason. Retain the old entry in the ledger.
- A closure condition states an observable outcome and named verification, not
  an implementation. Put an optional design recommendation in a separate
  field. If a reviewer prescribes a design, record that reviewer as the
  prescription author. Classify an inherent regression from following it as
  `prescription-induced`, record the causal evidence, and have Guardian revise
  the prescription; do not label that regression an executor regression. The
  regression still receives the ordinary materiality and gate decision.
- Each accepted residual risk needs a named authority and owner, explicit
  acceptance evidence, and the scope of what was accepted at acceptance time.
  Guardian may record acceptance but cannot grant it on its own authority.
  Unless the frozen task names a delegate with explicit authority, route
  acceptance to the human user through the interface node. Apply this rule
  consistently to comparable risks.
- Track the evidence owner separately from the reviewer. Reviewer packets
  contain observations, evidence, and risk signals only; Guardian alone
  decides materiality, closure, rework, escalation, and convergence. A
  reviewer cannot satisfy an authority gap, accept risk, publish, or mark
  unavailable external evidence as closed without the named owner.
- Keep candidate identity separate from review packets: strip source identity,
  history, transport/session metadata, and timestamps before blind review.
  Guardian keeps the identity-bearing control envelope and records the mapping
  in the ledger; reviewers do not infer or reconstruct it.
- Before the single material batch, review the complete change and ledger.
  Freeze material IDs, scope/criteria, observable closure outcomes, and
  reproducible named cheap/full verification commands or targets. Change them
  only after an explicitly recorded scope or criterion change. After such a
  change, refreeze and review the full change before narrowing re-review.
- Mutant diffs, logs, gate receipts, and other proof artifacts must be readable
  by assigned reviewers before their packet is sent. Name each reviewer-visible
  path in the packet (a projection-relative alias in blind mode), and keep the
  real path in Guardian's control envelope when blind. If access cannot be
  verified, label the evidence a worker claim and keep the affected finding
  open; do not present the artifact as independently reviewed proof.
- Every round assigns each prior ID exactly one disposition: `recurrence`,
  `regression`, `closure`, `supersession`, `accepted risk`, `unchanged`, or
  `changed criterion`. Record new discoveries separately with fresh IDs.
  A missing or duplicate disposition invalidates the report. An `accepted risk`
  disposition is invalid without accepting authority, owner, evidence, and
  scope. Every round and final confirmation reports frozen scope, criteria,
  targets, the complete ledger, material batch, verification evidence, verdict
  (`CONVERGED`, `REWORK`, or `ESCALATE`/`BLOCKED`), and explicit next action.
- Use this required round-report record, with `none` for empty lists:

| Field                   | Required content                                                                                                                                                              |
| ----------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Round                   | `initial`, `rework 1 of 2`, or `rework 2 of 2`; initial is not a rework.                                                                                                      |
| Stage counters          | Packet and final-report lineages, stage owner, initial/correction number, reason and previous submission; keep blind linkage in Guardian's private control record.            |
| Freeze                  | Contract version, scope, criteria, targets, baseline, mode, mode owner/reason, and design/public boundary.                                                                    |
| Criterion changes       | Finding ID, old condition, new condition, reason, owner; never relabel a new or tightened condition as previously frozen.                                                     |
| Prior ID dispositions   | Every prior ID exactly once with one of the seven dispositions; list new discoveries separately with fresh IDs and link supersession or changed criterion to its predecessor. |
| Accepted residual risks | Finding ID, risk scope, accepting authority, named owner, and acceptance evidence.                                                                                            |
| Proof access            | Reviewer-visible path or blind alias for each artifact and access result; inaccessible evidence is a claim.                                                                   |
| Baseline comparison     | Head-versus-base latency and liveness evidence, plus loss/corruption assessment.                                                                                              |
| Ledger and decision     | All ID reconciliations, material batch, named verification results, verdict, and next action.                                                                                 |

- For substantive verdicts, apply the `decision_quality_check` prompt block in
  `tmux-a2a-postman skills postman-review report`. Only Guardian's synthesis
  may update or close the authoritative ledger; reviewer use is packet-local.
