# Agent environment comparison: research increment 1

**Evidence checked: 2026-11-02 UTC.** This is a dated research increment for
[#891][research-issue], not a parity score,
implementation roadmap, or product benchmark. All seven questions and ten
cross-cutting lenses remain together. The issue remains open for further
evidence and experiments.

[research-issue]: https://github.com/i9wa4/tmux-a2a-postman/issues/891

The useful next step is to measure whether existing transport, Skills, runtime
controls and artifacts produce accepted work with less human attention.
Current evidence does not justify a new Postman task database, permanent
supervisor, trust ledger, review engine, or automatic policy modification.
Recovery of a substantive task, semantic fidelity through delegation, and
human acceptance require experiments beyond mail delivery tests.

## 1. Agreed direction: composable tools and Skills

The [maintainer's synthesis][continuity-comment] makes **continuing the same
human-owned work** across conversations, AI clients, machines and execution
environments the organizing hypothesis. Concurrent multi-agent execution is a
separate, workload-dependent optimization. A stronger model might need fewer
coordinators without removing distributed data, intermittent devices, separate
credentials, execution failures or human context switching. These are design
hypotheses to test, not predictions of a winning product or organization.

[continuity-comment]: https://github.com/i9wa4/tmux-a2a-postman/pull/974#issuecomment-6569719829

Keep four concerns explicit:

| Concern                | Desired coherence                                                                              | Boundary to retain                                                                     |
| ---------------------- | ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| Human access           | Inspect and direct the same work from supported devices.                                       | Actual interface availability and who may inspect the data.                            |
| Work continuity        | Recover goals, exclusions, decisions, evidence revisions, unresolved matters and next actions. | Authorized resumable context, without reproducing private conversation or model state. |
| Execution coordination | Select an appropriate environment and bind its result to the intended input/revision.          | End-to-end outcomes at the intended source/output environments.                        |
| Authority              | Identify the owner and allowed action at each step.                                            | Separate credentials, data-sharing permissions and execution approvals.                |

The first three can form a coherent job experience while authority remains
explicit. A shared Issue or a relayed AI proposal grants no new data-sharing
permission or human authorization. Distinguish the **accountability graph**
from the **evidence-reference graph**: hierarchical delegation can coexist
with authorized direct inspection of original requirements and artifacts.
References can reduce repeated paraphrasing while preserving Postman's routes,
POP/full-archive rules and mailbox isolation.

Unix-style composition means low coupling, inspectable inputs/outputs,
replaceable components and robust composition. Postman supplies reliable mail,
notification and exact execution decisions; environment-owned records retain
objectives and evidence; Skills supply useful procedural knowledge and judgment.
Receipts, operation IDs, revision bindings and locks belong in deterministic
tools where correctness needs them. Demonstrated mechanical invariants can
justify substantial core improvements; a small command count is not the goal.

Extract dotfiles' failure-prevention principles into reusable guidance,
including removal of obsolete workarounds, without copying fixed role names,
paths, launchers or its entire instruction corpus. Test which missing
procedural knowledge helps and which redundant requirements add work. Skills
and repository context files are not interchangeable experimental treatments.

Judge progress toward frontier systems by accepted outcomes, continuity, human
attention and total resource/maintenance/export cost. The experiments below
separate actor count, durable records and environment switching. This research
implements no new Skill, work-state subsystem or synchronization service.

## 2. Baseline, evidence and confidence

The original comparison snapshot used landed Postman
[`73787791c5594381b7018660090cda0bab399a54`](https://github.com/i9wa4/tmux-a2a-postman/tree/73787791c5594381b7018660090cda0bab399a54),
including #969's filesystem channel and #970's team-manager Skill. The dotfiles
policy inspected is landed
[`96d8ad5325637bb5485675f301c58b6a08e38609`](https://github.com/i9wa4/dotfiles/tree/96d8ad5325637bb5485675f301c58b6a08e38609).
Source presence does not establish installed-fleet activation. In particular,
that dotfiles revision pins older Postman source; its pinned installation must
not be credited with every capability of the newer upstream baseline.

The directory correction in [#972][directory-fix] and invocation diagnostics in
[#973][invocation-diagnostics]
have since landed in commits `89ab9baf816576bf6420c83840b79ba985e01dd0` and
`a3e4f210bc03344fe61807e7b39daa1fc4b504c3`, respectively. Their native tests and
Linux/macOS CI passed. They improve native workspace selection and invocation
evidence; they do not establish authoritative task ownership or acceptance.
Future experiments must pin their actual source/environment revisions rather
than treating this original snapshot as an installed-fleet report.

[directory-fix]: https://github.com/i9wa4/tmux-a2a-postman/pull/972
[invocation-diagnostics]: https://github.com/i9wa4/tmux-a2a-postman/pull/973

Evidence types used below:

| Type                           | What it supports                                            | What it does not establish                              |
| ------------------------------ | ----------------------------------------------------------- | ------------------------------------------------------- |
| Inspected source/specification | A named implementation or contract at a pinned revision     | Correct operation across all deployments                |
| Official product documentation | Documented behavior and maturity as retrieved on this date  | Independently reproduced reliability, cost or speed     |
| Vendor engineering report      | A reported design or case study                             | Controlled comparative advantage                        |
| Research paper                 | Authors' results in the stated tasks, models and budgets    | Transfer to this environment or a deployed organization |
| Local deterministic test       | An exercised protocol/runtime invariant in an owned fixture | Model comprehension, judgment or sustained autonomy     |
| Proposed experiment            | A falsifiable next investigation                            | A result already obtained                               |

**High** confidence means the narrowly stated fact is directly visible in
source, a contract or an exercised local check. **Medium** means official
documentation or a reported experiment supports it but independent operation
was not reproduced. **Low** means local transfer, comparative effectiveness or
current availability remains unknown. Confidence in reading a paper is not
confidence that its reported advantage will transfer here.

No competing product was installed or benchmarked in this pass. No paid model
trial, live-session modification, speech trial, human-review timing study or
cross-product throughput measurement was performed. The external source
registry below records mutable documentation as retrieval snapshots rather
than pretending it has immutable release pins.

## 3. What the local environment already establishes

| Mechanism and local evidence                                                                                                                                                          | Supported conclusion                                                                                                                                                             | Remaining limit                                                                                                                                  |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| [CLI contract](../../help/cli-spec.md), [file-first architecture](../design/file-first-architecture.md), [#937 acceptance audit](../validation/937-acceptance.md)                     | Durable mail, exact claims/replies, native ownership, bounded foreground notification and saved observations have explicit contracts and deterministic process checks. **High.** | Saved screen movement, POP and reply correlation cannot prove work progressed or met acceptance criteria.                                        |
| `internal/workspace/execution_test.go`: `TestExecutorCancellationAndLateReplyCannotAuthorizeNewCall`, `TestExecutorEqualConcurrentInvocationsHaveIndependentApprovals`                | An old execution decision cannot authorize a new invocation; equal concurrent requests have separate approval correlation. **High.**                                             | This is invocation authority, not a general logical-task/attempt lifecycle or duplicate-side-effect recovery guarantee.                          |
| [External-channel contract](../design/issue-767-external-channel-contract.md); `external_test.go` crash boundaries, superseded scopes, binding isolation and revised-decision closure | Existing mail mechanisms support bounded human exchanges, exact pending decisions and catch-up after interrupted publication. **High.**                                          | Local filesystem identity does not authenticate a remote human; no voice bridge, scheduler or generic cancellation protocol follows from this.   |
| [Team-manager Skill](../../help/postman-team-manager/SKILL.md), its offline decision scenarios, recall and usage Skills                                                               | Explicit management can inspect direct reports, retain uncertainty, ask one targeted question and require actual evidence. **High** for authored policy and offline checks.      | Effective model adherence and reduced supervision remain **unmeasured**. No automatic monitoring is supplied.                                    |
| [Recursive-session trial](../validation/recursive-sessions-trial-runbook.md), deterministic terminal-agent suites                                                                     | Real tmux input drives receiver-owned POP, full archive reads and exact return messages across direct parent/child interfaces. **High.**                                         | These authored deterministic behaviors do not demonstrate autonomous decomposition, semantic preservation or reviewer quality.                   |
| Pinned dotfiles `postman.md`, guardian/critic policy, Logbook/artifact conventions and Nix skill installation                                                                         | Goals, review routing, evidence and durable work notes already have owners outside transport. **High** for source policy.                                                        | An instruction or successful fixture is not proof that deployed agents follow it; cross-runtime permission equivalence needs its own evaluation. |
| [Pinned Issue mailbox bridge][mailbox-source], inspected import/cache/export paths                                                                                                    | Source at dotfiles `d25e058` implements message receipts, immutable cached comments and destination-specific export. **High** for inspected code.                                | Maintained-document synchronization, organization-wide routing and deployed behavior are not established.                                        |

[mailbox-source]: https://github.com/i9wa4/dotfiles/blob/d25e05857eb88bad7d15d14bef95ddf80855dc74/scripts/gh-issue-mailbox.py

The current six operational commands, optional filesystem-channel commands and
read-only overview expose no generic task-acceptance, task-priority or
organization-cancellation API. That is a **confirmed boundary**, not an
automatic demand to add those APIs. The retired #704–#706 and trust-ledger
roadmaps remain retired. Skills remain separate from CLI help, as clarified in
the latest #891 comment; the earlier bundled-manual proposal is superseded.

## 4. Seven questions: findings and decisions

The classifications below use #891's existing distinctions: **proven gap**,
**unmeasured capability**, **deliberate trade-off**, and **another layer's
responsibility**. A proven absence of a transport feature can simultaneously
be an intentional boundary.

| Question                                    | External evidence and narrow finding                                                                                                                                                                                                                                             | Local comparison and classification                                                                                                                                                                                                                    | Decision, experiment and owner                                                                                                                                                                                                                                                               |
| ------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Q1. Work surviving replacement**          | Orca/Symphony guard or reconcile work; ScaleMAS persists linked messages beyond their producers; LongMemEval separates five memory abilities. [S1–S3, S35, S37] **High** for inspected specifications, **Medium** for reported studies.                                          | Mail correlation and history persist; recovery of the original work, current owner/revision and valid acceptance after replacement is **unmeasured**. Chat recall is not authoritative task state. Record/runtime ownership remains **another layer**. | E1 first replaces one agent on the same machine, then changes machine/runtime under matched mail-only and referenced-record conditions. Recover authorized work context and verify effects; optional Logbook remains one backend. Propose core changes for reproduced mechanical violations. |
| **Q2. Reasoning versus executable control** | Workflow Runs and Agent Framework provide executable control. SkillsBench reports paired curated-Skill gains; AGENTS.md v2 finds context files do not generally improve success and add cost. Different settings prevent combining their effects. [S4, S10, S38–S39] **Medium**. | Mail/approval mechanics are deterministic; decomposition and review judgment remain Skills. Which procedural knowledge reduces coordination work is **unmeasured**. Useful functions may belong in tools; no workflow language follows from this gap.  | E2 separates a small repeated procedure from judgment, with matched facts and budgets. Measure actual receiver behavior and coordinator work before choosing a script, existing Skill or external runner; retain the simplest successful composition.                                        |
| **Q3. Useful throughput from teams**        | Cursor/Gas Town describe organization; Agensh/MASS/scaling results depend on task, capability and budget. ScaleMAS studies bounded shared-evidence access under its own task setting. [S7, S9, S11–S13, S35] **Medium** for reported benefits.                                   | Recursive routing exists; accepted work per resource and per human intervention is **unmeasured**. Continuity can matter with one agent. No general need for more workers, fixed hierarchy or a ranking datastore is established.                      | E3 independently compares one agent, a small team and hierarchy on sequential/separable tasks at matched total resources. Count setup, prompts, retrieval and handoffs; returning to one agent is a valid successful choice.                                                                 |
| **Q4. Independently judgeable results**     | Outcomes and harness reports support inspectable review; DevicesWorld finds source/output confusion and partial actions mistaken for complete cross-device work. [S5, S8, S20, S36] **Medium**; device-benchmark scores do not estimate this workflow.                           | Guardian/critic and artifact contracts exist. Delivery, a local success or a plausible DONE do not establish original-task acceptance across environments. Human review effort and false completion remain **unmeasured**.                             | E4 checks actual integrated state at the intended environments, original constraints and exact evidence revisions, with independent-first review and seeded partial/stale success. Measure correct human acceptance and reconstruction cost; preserve dissent.                               |
| **Q5. One conversation controlling work**   | GPT-Live handles simultaneous conversation/backend work; the application owns permissions, progress and whether interruptions cancel work. A2UI describes interface actions, not backend authority. [S15, S23–S24] **Medium.**                                                   | #767 has stable scoped pending choices, while general pause/reprioritize/cancel is absent. This is a **proven interface gap** and **another layer's responsibility**, not missing mail transport. Voice maturity is **unknown** here.                  | E5 first uses deterministic text events against one pending action, including delayed old approval and reconnect. Human-interface owner decides playback interruption versus dispatch pause versus task cancellation; only then assess Japanese audio.                                       |
| **Q6. Experience becoming improvements**    | Dreaming separates curated memory; SkillsBench and AGENTS.md study different context treatments. Self-evolving Skills can overfit task details and lose training gains on held-out tasks. [S2, S6–S7, S38–S40] **Medium** for reported findings.                                 | Incident notes, Skills and tests provide an adoption path; benefit on later work is **unmeasured**. More instructions or a favorable judge impression do not establish improvement. Automatic policy rewriting remains a rejected **trade-off**.       | E6 uses discriminating probes, a reviewed versioned diff, held-out execution and regression controls. Evaluate deleting obsolete rules as well as adding procedures; extract a failure-prevention principle rather than a task-specific universal rule.                                      |
| **Q7. Governable resources and authority**  | Paperclip documents budget stops and incomplete accounting; managed runtimes separate credentials and sandbox execution. [S3, S14, S22] **Medium.**                                                                                                                              | Exact reviewed execution is not an OS boundary; Postman does not collect provider billing or enforce an organization budget. **Another layer**, with an explicit **core trade-off**. Current cross-provider cost coverage is **unmeasured**.           | E7 uses synthetic priced/unpriced events and native runtime limits; pause new dispatch while preserving work. Runtime/provider owner proves actual stop behavior and uncertainty handling before claiming a spend cap.                                                                       |

## 5. Ten lenses applied to the same investigation

These refine the seven questions; they are not ten new implementation issues.
Every experiment is **proposed and not run** in this research increment.

| Lens                                                            | Evidence, local implication and confidence                                                                                                                                                                                                                                         | Smallest experiment and decision owner                                                                                                                                                                                                                   |
| --------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **C1. Task-adaptive topology** (Q2/Q3/Q7)                       | The scaling v3 abstract covers 260 configurations and reports task/capability-dependent effects; MASS and Agensh use different settings. [S11–S13] **Medium.** No universal preference for centralized/decentralized or larger teams follows.                                      | E3 isolates actor count from records and environment switches. Compare accepted outcomes at matched total resources, including a single-agent option and coordination cost.                                                                              |
| **C2. Meaning-preserving handoff** (Q2/Q3)                      | Routed Graph Handoff reports representation-dependent gains/regressions; persistent original references offer another hypothesis. [S16, S35] **Medium.** Exact reply IDs do not prove retained meaning.                                                                            | E2 compares prose/structured/hybrid handoffs and authorized source references across hops. Measure receiver actions, omissions and retrieval cost; direct evidence access never bypasses mail routes, POP or full-file reading.                          |
| **C3. Independent reasoning/global objectives** (Q3/Q4/Q7)      | PoTRE varies reasoning methods; the AI-organizations study highlights organizational objective/ethical failures. [S17–S18] **Medium.** Different role or provider names alone do not prove independent review.                                                                     | E4: independent-first versus justification-first review of seeded local and global defects; record correlated misses and discarded dissent. Guardian/critic policy owner decides the review order.                                                       |
| **C4. Sufficient, current, sourced context** (Q1/Q3/Q6)         | Agentic RAG/Graphiti expose context/provenance mechanisms; LongMemEval separates extraction, multi-session/temporal reasoning, updates and abstention. ScaleMAS bounds message retrieval. [S19, S25, S35, S37] **Medium/High** within each evidence type.                          | E1/E2/E4/E6 compare a small current-work index and selected source references with whole-mailbox reading. Grade current-revision choice, missing constraints and unknowns; measure read tokens/time and coordinator burden before adding infrastructure. |
| **C5. Verified failure attribution** (Q4/Q6)                    | ErrorProbe and MP-Bench support discriminating probes and uncertain attribution. Self-evolving-Skill results caution that a judge's finished-skill ranking does not validate an individual edit. [S26–S27, S40] **Medium**; invocation traces alone are not causal proof.          | E6 injects runtime and delegation faults, demands a distinguishing probe, then executes held-out/regression controls for the proposed edit or deletion. Keep ambiguity explicit and never replay an archived command as diagnosis.                       |
| **C6. Parallel integration and executable evidence** (Q1/Q3/Q4) | GitButler parallel branches share runtime; harness evidence improves inspection; DevicesWorld evaluates actual cross-environment final states. [S20–S21, S36] **Medium.** Separate commits or successful local actions do not prove integration.                                   | E4 combines opposite sides of a mock API in owned worktrees/environments, with source/output targets and original acceptance conditions. Measure semantic defects, integration cost and teardown.                                                        |
| **C7. Complete non-coding workflows** (Q3/Q4/Q7)                | Ironclad evaluates business-workflow configuration using multiple criteria; rubric averages and estimated duration are not full-task success or observed customer savings. [S28] **Medium.** Local evidence is predominantly coding/transport.                                     | E7: simulate threshold/exception/human-approval cases in a disposable application and inspect final state. Domain owner defines acceptance before running it; no live business action.                                                                   |
| **C8. Delegation/memory trust** (Q3/Q6/Q7)                      | Containment engineering separates low-trust content, tool authority and runtime enforcement. [S22] **Medium.** Authentic mail or an agent summary does not grant human consent or factual truth.                                                                                   | E2/E6: relay a harmless instruction-like string from a low-trust document; require it to remain quoted data and exclude it from standing rules. Policy/runtime owners enforce actual permissions outside prompts.                                        |
| **C9. Stable voice/UI actions** (Q5)                            | GPT-Live separates speech from backend control; A2UI current v0.9.1 and candidate v1.0 must not be conflated; the voice guide grades resulting application state. [S15, S23–S24] **Medium.** #767 already supplies useful exact decision scope, not voice or generic cancellation. | E5: optional view and text/voice adapters refer to one current pending action and scope; test revision, delay, duplicate answer and reconnect. Interface owner rejects stale actions and checks the final state.                                         |
| **C10. Repeated reliability/fair measurement** (all Qs)         | Any-success/all-success differ and infrastructure changes scores. SkillsBench and AGENTS.md use different tasks/treatments; self-evolving-Skill training gains need held-out checks. [S29–S30, S38–S40] **Medium**; no universal combined effect.                                  | Apply the common protocol to E1–E7, keep factors and study versions separate, include passing/failing controls, and report all trials plus unknown costs and grader disagreements.                                                                       |

## 6. Bounded experiments and decision gates

Owners here name existing **responsibilities**, not new agents or services.
The maintainer chooses a person/session for an explicitly invoked experiment.
These plans authorize neither paid provider trials nor live-fleet actions.

| ID / first useful outcome                      | Setup, controls and falsifiable checks                                                                                                                                                                                                                                                                                                                                 | Initial decision and stop condition                                                                                                                                                                                                                                                        |
| ---------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **E1 — restart without false completion**      | Begin with one replacement agent on the same machine in mail-only and referenced-record arms; include no-crash controls. After observing an effect, replace A with B and deliver A's old-revision DONE/failure/approval. Then vary machine and AI runtime separately. Grade original criteria, current owner/revision, unresolved effects and execution count.         | Workflow/runtime owner first reuses recall and records. Fail if stale evidence changes current authority, masks partial completion or repeats an uncertain effect. Recover the work rather than private conversation/model state; propose core remedies only for reproduced invariants.    |
| **E2 — preserve constraints through handoffs** | Carry exclusions, uncertainty and low-trust quoted text across three hops using prose/structured/hybrid handoffs. Match facts, permissions and budget in mail-only/referenced-record arms. Separately vary runtime and selected original references versus whole-mailbox reading. Grade actual actions, preserved constraints, read cost and coordinator work.         | Skill owner preserves routes, POP/full-archive rules and data-sharing boundaries. Stop on authority promotion or inaccessible evidence treated as complete. A fixed actor validates mechanics, not comprehension; no retrieval database is required.                                       |
| **E3 — choose the smallest effective team**    | Independently compare single-agent, small-team and hierarchical execution on one sequential and one separable task. Match total calls/tokens/time, model/version, facts and acceptance. Hold work-record and environment conditions fixed; count setup, duplicate instructions, retrieval and coordinator load.                                                        | Workflow owner chooses accepted outcomes per total resource and human attention. A switch back to one agent is a valid capability. Do not attribute record/resumption benefits to extra agents or prefer a topology from unmatched tasks.                                                  |
| **E4 — accept integrated evidence quickly**    | Seed local/global defects, changed requirements, old-revision DONE, inaccessible evidence, partial cross-environment success and uncertain publication. Compare record and review-order arms with matched facts, then selected references versus full-mailbox reading. Check original criteria and actual integrated state at the intended source/output environments. | Review/integration owner grades false acceptance, dissent and correct current-revision selection; measure human reconstruction, read effort and reproducibility. Preserve offline-export checks and explicit authorization; visual polish and local successes cannot establish completion. |
| **E5 — control the correct pending action**    | Use a disposable backend and #767's current binding/exchange/scope. Revise action X, delay its old answer, interrupt playback, request pause/cancel, disconnect/reconnect and repeat an answer. Compare with uninterrupted text control.                                                                                                                               | Interface owner checks actual action count/current state and explicit cancellation acknowledgment. Stop if old approval affects revised work. Audio/model evaluation follows only after deterministic authority tests, with separately authorized Japanese recordings/provider usage.      |
| **E6 — improve from verified incidents**       | Inject runtime/delegation/stale-context faults and require a discriminating probe. Compare a reviewed versioned Skill/tool/test edit with its baseline, including deletion of an obsolete rule. Execute held-out failure and valid regression controls without leaking the diagnosis or expected answer.                                                               | Skill/diagnostic owner adopts only reproducible improvements with rollback and maintainer review. Keep causal uncertainty; a training gain, longer prompt or judge impression of one edit cannot authorize universal rule adoption.                                                        |
| **E7 — bounded non-coding execution**          | Simulate an approval threshold, exception and human decision; add priced, unpriced and delayed accounting events with a small concurrency limit. Pause new dispatch, retain ownership, resume once and verify final application state.                                                                                                                                 | Domain/runtime owner checks no unknown cost becomes zero, no duplicate effects, and no action exceeds authorization. Runtime stop and billing bounds need backend evidence; a budget badge or prompt is insufficient.                                                                      |

### 6.1. Shared work-state comparison for E1, E2 and E4

The [maintainer's review comment][work-state-comment] proposes a hypothesis:
a portable convention for using a durable work record may improve recovery,
handoffs and acceptance beyond what mail correlation alone provides. This is
**unmeasured**, not a request to implement a ledger or a new Skill in this PR.

[work-state-comment]: https://github.com/i9wa4/tmux-a2a-postman/pull/974#issuecomment-6568131376

Separate record use, actor count and environment changes. Establish the
same-machine replacement control first; change machine or AI runtime in later
matched comparisons rather than confounding continuity with parallelism.

Use two explicit arms with the same workload, authorized facts, visibility,
model/runtime, budget and hidden acceptance checks:

- **Mail history alone:** agents use their permitted mail archives and existing
  correlation/recall mechanisms to reconstruct current work state.
- **Mail plus a referenced work record:** agents receive the same underlying
  facts plus a stable reference to an environment-owned canonical record. The
  workflow asks them to read its current revision and reconcile it with the
  handoff before acting or assessing completion. Do not give this arm extra
  privileged facts or an evaluator's ground-truth answer.

Observe whether agents recover the original objective, constraints, current
owner/attempt, progress, artifact/evidence references, unresolved decisions and
acceptance status. This is an evaluation checklist, not a new transport schema.
Record creation, maintenance, lookup and reconciliation cost as well as useful
outcomes. Test stale, conflicting and inaccessible records too: a reference
alone must not grant authority, erase uncertainty or silently choose a new
canonical record. Keep side-effect replay and exact approval checks independent
of the workflow comparison; the same core protections apply to both arms.

For E1 measure stale/late reports corrupting the current attempt, lost original
criteria and duplicated effects after replacement. For E2 measure omitted or
changed constraints in the receiving worker's actual behavior across hops, not
just matching text or IDs. For E4 measure correct acceptance/rejection, evidence
coverage, review time, archive/conversation reopenings and human interventions.
A scripted fake can validate the scenario and transport, but cannot demonstrate
semantic improvement or lower human review effort.

A concrete candidate is an **optional Logbook-backed workflow Skill**: discover
and inspect the correct existing work record, include its reference in a
substantive handoff, reconcile the record when receiving/resuming work, and
assess returned evidence against the original completion conditions. Existing
Logbook operations would own storage and editing; the shared procedure would
own when to read, reconcile and report. Markdown, an Issue or another backend
could serve the same role. Decide between an existing Skill extension, an
integration example and a separate workflow Skill only after evidence of value.

A fixed storage default is compatible with testing this idea; storage
agnosticism need not block a first trial. The Postman XDG state area is one
candidate raised by the maintainer, not an allocated path in this PR. Before
adopting a location, check existing mailbox/workspace naming, permissions,
ownership and retention so work records neither collide with mail nor gain
transport authority. This report defines no new directory, writer or schema.

If the referenced-record arm improves original-goal preservation, rejection of
stale completion and independent human judgment at an acceptable total cost,
revise Q1/Q3/Q4's local-capability assessment to credit the composed workflow.
That may bring the whole environment closer to systems with durable work
context without expanding transport into orchestration. If it adds conflicting
truth, false confidence or maintenance cost without benefit, refine existing
practice or defer the Skill. It would not by itself resolve sandbox, budgeting,
runtime replacement or cancellation capabilities. No result or implementation
is claimed here.

### 6.2. Work portability across clients, task tools and hosting

The [follow-up review][portability-comment] extends the hypothesis: a maintained
Markdown work record with explicit ownership, revision and evidence references
may let work continue after its machine, AI client, task UI or hosting service
is replaced. Measure whether the work remains understandable and actionable.
Markdown can carry meaning across tools; receipts, identifiers, revision checks
and locks still belong in deterministic helpers where correctness needs them.
Extract dotfiles' failure-prevention principles into reusable procedures.

[portability-comment]: https://github.com/i9wa4/tmux-a2a-postman/pull/974#issuecomment-6569065699

Vibe Kanban supplies concrete planning/workspace/review patterns for comparison.
Its inspected README also explicitly says it is sunsetting. [S33] Treat those
patterns as evidence to compare and verify current availability before a trial;
do not infer a product's future or recommend a migration from feature counts.
Compare switching, maintenance and human-attention costs using the same work.

Issue Artifacts is a concrete candidate for separating conversation from a
maintained document. The 2026-10-28 announcement documents Markdown, HTML and
link artifacts, individual references and edit history. The CLI documents file
export and an earlier version selected with `--version`. [S31–S32] These are
documented preview capabilities, not locally exercised synchronization or
client-parity guarantees. In particular, the announcement says GitHub App
creation/viewing of HTML is still coming; mobile rendering and event support
need verification. Link downloads are `.url` files, not copies of their targets.

Keep the responsibilities explicit in a trial:

- Issue comments carry requests, proposals, questions and change notices.
- A maintained Markdown record carries the current understanding of the work;
  an exact published revision binds the context used for delegation or review.
- Optional read-only HTML presents selected records and can be replaced.
- The inspected `gh-issue-mailbox` file/comment bridge supplies message delivery
  with the limits in Section 6.4. Postman supplies local transport, and Skills
  describe when to read, reconcile and report. Verify each AI client's actual
  supported interface before a trial. Artifact fetch/publication can initially
  use existing `gh` commands explicitly, on demand.

A local draft and an explicitly published shared revision can coexist. Select
the update owner and authoritative revision for each record; other agents may
return proposals for explicit adoption. Local-authoritative publication is
another valid arm. A single-writer convention is not distributed locking.
Artifact publication and its announcing comment are separate effects: test
uncertain publication, interrupted announcement, retries and concurrent edits.
This proposal adds no bidirectional sync engine, scheduler or task service.

A machine/session mailbox may carry unrelated topics. Rotating that mailbox
must not close its work or reset its identity: retain original request and
work-record references without imposing a task schema on every conversation.
Keep proposals, reports, evidence and human authorization distinct across
Issue, file and parent-agent hops. Provenance alone grants no execution right.

After the same-machine replacement control, extend Section 6.1 with one
bounded work item and a separate machine/runtime-switch condition. Match the
underlying facts, model budget, original acceptance criteria and authorized
visibility across the mail-only and referenced-record arms:

1. Begin on one machine and select a Markdown work record with an exact
   revision reference, its original objective, constraints and evidence.
2. Have another supported AI client/runtime inspect that revision and return a
   proposal through the mailbox; explicitly adopt or reject it.
3. Stop the original agent and resume elsewhere. Inject a late DONE for an
   older revision, an inaccessible reference and an uncertain publication.
4. Remove GitHub access. Give the replacement an authorized export of the
   selected records, provenance and required evidence, then test whether it
   can understand and continue the work. Identify inaccessible link targets,
   attachments and other missing dependencies rather than assuming export
   completeness from readable Markdown.

E1 grades recovery and current ownership/revision after replacement. E2 grades
retained constraints and proposal/authority boundaries across client hops. E4
grades correct human acceptance and reconstruction effort with another UI or
the offline export. Record false completion, duplicated effects, unauthorized
scope changes, human reconstruction time and record creation/update/export
cost. A rendered dashboard or a Markdown file opening is insufficient evidence.
Keep hidden passing/failing controls and the mail-only arm; fixed actors prove
protocol behavior only. No paid/live trial, publication integration, mandatory
schema, new Skill or GitHub dependency in Postman core is implemented here.

### 6.3. Selective evidence and useful procedural knowledge

ScaleMAS v1 makes persistent, addressable linked evidence and bounded reading
a relevant comparison: agents publish, search and visit messages, which can
outlive their producers. [S35] Its reported citation-based ranking is a quality
proxy in its experiments, not truth or authority. A GitHub Issue does not
implement that datastore, and these findings do not require importing it.

A shared location alone can still overload readers or a top-level coordinator.
Start the E2/E4 comparison with a small current-work index and explicit source
references, then compare selective retrieval with reading the whole permitted
mailbox. The index is navigation, not an unquestionable source of truth. Keep
underlying evidence available and test stale indexes, changed requirements and
missing permissions. Count tokens/read time, missed constraints, stale-revision
adoption and the coordinator's work. Every claimed Postman message still gets
its required complete-file reading; vary selection of additional authorized
references, never truncation of POP output or bypass of mailbox isolation.

LongMemEval v2's distinct memory abilities motivate separate recovery checks,
not an engineering-task success estimate. [S37] SkillsBench v4/release 1.1 uses
paired curated-Skill evaluation; AGENTS.md v2 studies repository context files
in different settings. [S38–S39] Do not combine their numbers or infer that
packaging instructions guarantees benefit. Compare useful missing procedures
with redundant context at matched budgets and grade behavior.

For E6, retain a reproducible incident, a discriminating probe and the proposed
principle behind a versioned change. Self-evolving-Skill v2 results warn that
training gains can overfit task-specific columns, paths or output conventions;
a judge's view of a single edit is insufficient. [S40] Execute held-out and
regression cases and evaluate deletion of obsolete requirements too. This
research changes neither live Skills nor operating rules automatically.

### 6.4. Inspected mailbox boundary and explicit artifact exchange

The relevant implementation is dotfiles' pinned
[mailbox script][mailbox-source], not an assumed separate repository. [S34] It
exposes an explicitly invoked foreground Issue-comment poller with import/export
directories and a local per-Issue state lock. Source inspection establishes the
following code paths; this research did not execute a bridge trial or verify
deployed behavior.

| Inspected path               | What the source does                                                                                                                      | Consequence for the proposed trial                                                                                                                            |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `import_files`               | Records source absolute paths and their outbox identities; skips already posted/pending identities and preserves an existing outbox copy. | Editing a previously imported document at the same path does not publish successive maintained revisions.                                                     |
| `mirror_comments`            | Uses paginated conditional requests and cached comment bodies; a changed cached body raises an error.                                     | Publish/fetch the document explicitly and use a new comment to announce its exact revision; old-comment edits are not new mail.                               |
| `export_files`               | Uses destination-path/comment-ID receipts, preserves uncertain outcomes and skips IDs in this instance's sent archive.                    | Test destination selection, uncertain delivery and multiple-integration feedback loops; local self-echo suppression is not global routing or remote identity. |
| `post_claim` / `post_outbox` | Persist claim/content bindings and posting intent; uncertain API results require inspection.                                              | Keep publication and announcement as separate effects; do not infer an atomic document transaction or blindly repeat uncertain writes.                        |

Use this existing bridge for messages, Postman for local transport and Skills
for workflow judgment. Start with explicit artifact operations through supported
interfaces. Test a published document with a missing announcement, an uncertain
write, concurrent proposals, wrong-recipient delivery and integrations sharing
one Issue. Resolve using the correct source/revision and receipts, with explicit
unknowns. Neither a local lock nor an AI suggestion grants human authorization.
No universal document synchronizer, task schema or organization router follows
from this source inspection.

### 6.5. Common measurement protocol

For each experiment record the exact source/Skill/model/runtime revisions,
task input and hidden acceptance checks, environment CPU/memory limits, seed
where applicable, total calls/tokens/cost or **unknown**, elapsed time, queue
and approval waits, lost/duplicated work, false completion, human interventions,
lower-pane visits and decision latency. Count configuration/setup/maintenance
cost, not only the steady-state run. Give code and domain graders a known
passing and known failing control; retain disagreements rather than silently
choosing the favorable judgment.

Start with a small exploratory sample (for example, ten isolated repetitions
per deterministic scenario, then a separately budgeted capability study).
Report numerator/denominator and every failed, incomplete or inconclusive run.
Any-success and all-success are different summaries; do not assume independent
trials when shared resources or retained state correlate failures. Small samples
cannot support a tail guarantee or a broad product ranking. Repeat/broaden only
to resolve a real uncertainty or after a relevant change.

**Recommended order:** E1/E2 first establish same-machine replacement with one
agent, then vary records and machine/runtime switching as separate factors.
E4/E6 test judgeable outcomes and evidence-based procedural improvement; E3
independently measures actor-count benefits and coordination cost. E5/E7 remain
separately owned interface/domain
experiments. Reuse existing mechanisms first. A future issue needs an observed
failure, smallest proposed remedy, owner and acceptance check; no automatic
seven- or ten-issue implementation backlog follows from this report.

## 7. Primary-source registry

All mutable web documents below were retrieved on **2026-11-02**. Dates in
titles describe publications, not a claim that a deployed service stayed
unchanged. Paper results were read, not reproduced locally. For pinned GitHub
files, the revision shown is the **commit**, not the returned file/blob hash.

| Source                                                                                                                                                                                                     | Version / evidence                                                                        | Narrow finding and confidence                                                                                                                                                                                                                                             |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S1** [Orca dispatch completion](https://github.com/stablyai/orca/blob/f307391637b53a0e05fe357f4942ac2718ada767/src/main/runtime/orchestration/db/dispatch-context/dispatch-completion.ts)                | Source inspected, commit `f307391637b53a0e05fe357f4942ac2718ada767`                       | Terminal transitions revoke dispatch capability; heartbeat/failure transitions are guarded. **High** for this code. Public orchestration documentation was inaccessible in this pass; broader behavior is unknown.                                                        |
| **S2** [Symphony engineering report](https://openai.com/index/open-source-codex-orchestration-symphony/), [SPEC](https://github.com/openai/symphony/blob/be10a1b79df723d6d7612b5651c8522704dafb2e/SPEC.md) | Report 2026-04-27; inspected Draft v1 at `be10a1b79df723d6d7612b5651c8522704dafb2e`       | Issue-driven workspaces and reconciliation; exact in-memory orchestration state need not persist. **High** for specification, **Medium** for reported operational benefits; no reproduced throughput claim.                                                               |
| **S3** [Managed Agents architecture](https://www.anthropic.com/research/managed-agents)                                                                                                                    | Vendor architecture report; original engineering URL redirects here                       | Session events, harness and sandbox are separate interfaces. **Medium.** Replacing compute and restoring authoritative work state are distinct concerns.                                                                                                                  |
| **S4** [Workflow Runs](https://platform.claude.com/docs/en/managed-agents/workflow-runs)                                                                                                                   | Mutable product documentation                                                             | Workflow code coordinates agents; handled child failures can coexist with a completed workflow. **Medium.** Runtime completion must not be equated with all task outcomes succeeding.                                                                                     |
| **S5** [Outcomes](https://platform.claude.com/docs/en/managed-agents/define-outcomes)                                                                                                                      | Mutable product documentation                                                             | Separate-context rubric evaluation is an available mechanism. **Medium** for documentation, **Low** for its effectiveness on this environment's work.                                                                                                                     |
| **S6** [Dreaming](https://platform.claude.com/docs/en/managed-agents/dreams)                                                                                                                               | Research preview, feature header `dreaming-2026-04-21`                                    | A separate curated memory output can be reviewed before adoption. **Medium.** This does not justify silent mutation of live operating rules.                                                                                                                              |
| **S7** [Cursor Projects](https://cursor.com/blog/projects)                                                                                                                                                 | Vendor report, 2026-09-10                                                                 | Coordinator/worker separation and shared project context. **Medium.** Scale and productivity statements are vendor reports, not controlled local measurements.                                                                                                            |
| **S8** [Cursor cloud computer use](https://cursor.com/blog/agent-computer-use)                                                                                                                             | Vendor report, 2026-02                                                                    | Inspectable computers and visual evidence support review. **Medium.** No local trial of VM recovery or human review savings.                                                                                                                                              |
| **S9** [Gas Town README](https://github.com/gastownhall/gastown/blob/466aa41c83ee73b1fa0eb79eb9d172fb1eca9476/README.md)                                                                                   | Source documentation inspected at `466aa41c83ee73b1fa0eb79eb9d172fb1eca9476`              | Git-backed work, town/rig roles, monitoring and merge services are documented; normal startup includes long-lived services. **High** for the README, not demonstrated reliability.                                                                                        |
| **S10** [Microsoft Agent Framework overview](https://learn.microsoft.com/en-us/agent-framework/overview/)                                                                                                  | Current product/library documentation                                                     | Explicit workflows coexist with agent reasoning. Go 1.26 support is preview with language-specific limitations. **Medium**; do not infer library capability parity across languages.                                                                                      |
| **S11** [Agensh](https://arxiv.org/abs/2609.26781v1)                                                                                                                                                       | Paper v1, 2026-09                                                                         | Large-scale shared-work coordination is a research result on specific tasks. **Medium** for reported findings, **Low** for local transfer; test-pass fractions are not full-task acceptance or cost-normalized gains.                                                     |
| **S12** [MASS](https://research.google/pubs/multi-agent-design-optimizing-agents-with-better-prompts-and-topologies/)                                                                                      | ICLR 2026 publication/abstract                                                            | Prompt and topology search can favor simpler pruned designs. **Medium** for reported results; no universal preferred topology follows.                                                                                                                                    |
| **S13** [Scaling Agent Systems](https://arxiv.org/abs/2512.08296v3)                                                                                                                                        | Paper v3, 2026-04-08; abstract explicitly reports 260 configurations                      | Matched tools/prompts/compute show task- and capability-dependent coordination gains and overhead. **Medium.** The configuration count is specific to v3; architecture preferences and local transfer remain task-dependent.                                              |
| **S14** [Paperclip costs/budgets](https://docs.paperclip.ing/guides/day-to-day/costs/)                                                                                                                     | Mutable docs; explicitly distinguishes v2026.1023.0 warning UI from a later fix           | Budget controls pause new work and retain assignments; incomplete/unpriced accounting remains explicit. **Medium.** In-flight overshoot and backend enforcement were not tested; an absolute no-overspend claim is unsupported here.                                      |
| **S15** [GPT-Live guide](https://developers.openai.com/api/docs/guides/live)                                                                                                                               | Mutable API documentation                                                                 | Client delegation permits an independent backend; the application owns permission, progress and cancellation. **Medium.** Full-duplex speech does not itself cancel backend work.                                                                                         |
| **S16** [Routed Graph Handoff](https://arxiv.org/abs/2608.25277v1)                                                                                                                                         | Paper v1                                                                                  | Handoff representation benefits vary by task; graph-only delegation also regresses in a reported setting. **Medium**, not demonstrated local semantic preservation.                                                                                                       |
| **S17** [PoTRE](https://research.google/pubs/potre-test-time-reasoning-inspired-by-cognitive-heterogeneity/)                                                                                               | TMLR 2026 publication/abstract                                                            | Different reasoning procedures and aggregation are studied. **Medium.** Role/vendor diversity alone is not the tested mechanism.                                                                                                                                          |
| **S18** [AI organizations](https://alignment.anthropic.com/2026/ai-organizations/)                                                                                                                         | Research report, linked paper `2604.10290`                                                | Simulated organizations expose global-objective and ethical failures. **Medium** for reported observations, **Low** for prevalence in real teams.                                                                                                                         |
| **S19** [Agentic RAG](https://research.google/blog/unlocking-dependable-responses-with-gemini-enterprise-agent-platforms-agentic-rag/)                                                                     | Vendor research/product report                                                            | Context sufficiency can guide further retrieval. **Medium.** Relevance, truth and authorization remain separate checks.                                                                                                                                                   |
| **S20** [Harness engineering](https://openai.com/index/harness-engineering/)                                                                                                                               | Vendor engineering report                                                                 | Per-worktree runtime observability and executable evidence improve inspectability. **Medium** for the case study, not a controlled productivity result.                                                                                                                   |
| **S21** [GitButler parallel agents](https://docs.gitbutler.com/ai-agents/parallel-agents)                                                                                                                  | Product documentation                                                                     | Parallel branches share working files/runtime; separate worktrees offer different isolation. **Medium.** Version-control separation is not independent execution.                                                                                                         |
| **S22** [Containment engineering](https://www.anthropic.com/research/how-we-contain-claude)                                                                                                                | Vendor report; original engineering URL redirects here                                    | Runtime boundaries, credential separation and treatment of untrusted content matter across agents. **Medium.** Trusted connector invocation does not make its contents authoritative.                                                                                     |
| **S23** [A2UI](https://a2ui.org/)                                                                                                                                                                          | v0.9.1 current; v1.0 candidate, formerly v0.10 draft                                      | Declarative client rendering; candidate v1.0 adds action IDs/action responses. **Medium.** Interface structure grants no backend permission, and candidate features are not current-release guarantees.                                                                   |
| **S24** [GPT-Live evaluation guide](https://developers.openai.com/cookbook/examples/audio/voice_agent_evaluation)                                                                                          | Mutable official evaluation guide                                                         | Grades corrections, authorization and final application state through progressively realistic audio/conversation. **Medium.** No Japanese/latency result was reproduced here.                                                                                             |
| **S25** [Graphiti EntityEdge](https://github.com/getzep/graphiti/blob/a9ef13f7e2cce23e92abd2f76947f42e270da25a/graphiti_core/edges.py)                                                                     | Source inspected at `a9ef13f7e2cce23e92abd2f76947f42e270da25a`                            | `episodes`, `valid_at`, `invalid_at`, `expired_at` and `reference_time` represent provenance/temporal distinctions. **High** for fields, not extraction accuracy. The inspected issue pin is not claimed to be current HEAD.                                              |
| **S26** [ErrorProbe](https://arxiv.org/html/2604.17658v1)                                                                                                                                                  | Paper v1                                                                                  | Backward attribution and executable probes ground retained diagnostic patterns. **Medium** for research findings; replay still requires authorization.                                                                                                                    |
| **S27** [MP-Bench](https://arxiv.org/abs/2603.25001)                                                                                                                                                       | Paper, retrieved snapshot                                                                 | Multiple perspectives and plausible causes complicate failure attribution. **Medium.** A persuasive single-cause narrative is not ground truth.                                                                                                                           |
| **S28** [OpenAI × Ironclad](https://openai.com/index/advancing-computer-use-with-ironclad/)                                                                                                                | Vendor/model evaluation, 2026-10-06                                                       | Multi-criterion workflow assessment broadens the domain beyond coding. **Medium.** Mean rubric scores and simulated duration estimates are not autonomous-task success or observed customer time savings.                                                                 |
| **S29** [Agent evaluation](https://www.anthropic.com/research/demystifying-evals-for-ai-agents)                                                                                                            | Official guidance; engineering URL redirects here                                         | Any-success and all-success summaries answer different reliability questions; evaluate actual outcomes. **Medium.** Grader calibration remains necessary.                                                                                                                 |
| **S30** [Infrastructure noise](https://www.anthropic.com/research/infrastructure-noise)                                                                                                                    | Vendor experiment; engineering URL redirects here                                         | Resource enforcement can change measured performance. **Medium.** Its reported score difference is not a universal correction factor.                                                                                                                                     |
| **S31** [Issue Artifacts announcement](https://github.blog/changelog/2026-10-28-issue-artifacts-are-now-in-public-preview/)                                                                                | Official announcement, 2026-10-28; public preview                                         | Markdown/HTML/links, individual references and retained edit history are documented. **Medium.** GitHub App HTML creation/viewing is described as coming soon; no cross-client/mobile/event or recovery behavior was reproduced.                                          |
| **S32** [Issue artifact download](https://cli.github.com/manual/gh_issue_artifact_download)                                                                                                                | Mutable official CLI contract                                                             | File export and `--version` for one earlier artifact revision are documented. Links download as `.url` shortcuts. **Medium.** This does not establish complete dependency export, conflict-safe publication or service-independent continuation.                          |
| **S33** [Vibe Kanban README](https://github.com/BloopAI/vibe-kanban/blob/7c3807084daad0d9d315064f60fa869de254f027/README.md)                                                                               | Source documentation inspected at `7c3807084daad0d9d315064f60fa869de254f027`              | Planning, agent workspaces and review are described; the README explicitly says the product is sunsetting. **High** for that source text, **Medium** for described features. Current service availability and portable work export were not exercised.                    |
| **S34** [Issue mailbox bridge source][mailbox-source]                                                                                                                                                      | Inspected dotfiles commit `d25e05857eb88bad7d15d14bef95ddf80855dc74`                      | Path-keyed imports, changed-comment rejection, destination-specific export receipts and local sent-ID suppression are visible. **High** for these functions; no general document sync, organization routing or deployed guarantee was established.                        |
| **S35** [ScaleMAS](https://arxiv.org/abs/2610.16120v1)                                                                                                                                                     | Paper v1, 2026-10-12; abstract inspected                                                  | Publish/search/visit operations over persistent linked messages bound retrieval and survive producers in the authors' setting. **Medium** for reported results, **Low** for local transfer; citations are no truth/authorization proof and Issues are not this datastore. |
| **S36** [DevicesWorld](https://arxiv.org/abs/2607.13465v1)                                                                                                                                                 | Paper v1, 2026-07-15; abstract inspected                                                  | Heterogeneous-device tasks expose source/output confusion and partial success without end-to-end completion. **Medium.** Borrow final-state verification; its benchmark score is not a CLI/mailbox performance estimate.                                                  |
| **S37** [LongMemEval](https://arxiv.org/abs/2410.10813v2)                                                                                                                                                  | Paper v2, 2025-03-04; abstract inspected                                                  | Extraction, multi-session reasoning, temporal reasoning, updates and abstention are separate conversational-memory abilities. **Medium.** Full chat retention is not demonstrated authoritative engineering-work resumption.                                              |
| **S38** [SkillsBench 1.1 report](https://www.skillsbench.ai/blogs/skillsbench-1-1), [paper](https://arxiv.org/abs/2602.12670v4)                                                                            | Release 1.1 report, 2026-06-16; paper v4, 2026-06-14; mutable report retrieved 2026-11-02 | Paired curated-Skill/no-Skill evaluation reports gains on its tasks; the paper's 18 configurations differ from the live leaderboard roster. **Medium.** These settings differ from AGENTS.md v2 and do not establish a universal or additive effect.                      |
| **S39** [Evaluating AGENTS.md](https://arxiv.org/abs/2602.11988v2)                                                                                                                                         | Paper v2, 2026-06-23; abstract inspected; later v3 is outside this cited snapshot         | Repository context files do not generally improve success in the reported settings and add cost; non-standard practices can still help. **Medium.** This does not establish that useful procedural Skills are ineffective.                                                |
| **S40** [Self-evolving Skills on held-out tasks](https://arxiv.org/abs/2609.39148v2)                                                                                                                       | Paper v2, 2026-10-07; abstract inspected                                                  | Training gains often transfer partly or fail; task-specific fixes can become inappropriate general rules. A finished-skill ranking does not validate one edit. **Medium** for reported findings; require held-out/regression execution before adoption.                   |

### 7.1. Unavailable or historical evidence

Herdr is discontinued according to the maintainer's #891 scope correction.
Existing [discovery](../design/herdr-readonly-discovery-spike.md) and
[security/licensing notes](../design/herdr-security-licensing-gates.md) remain
historical; no support revival or current compatibility claim is made.

The current `tmux-mac-voice` issue could not be fetched through the available
GitHub connector (404). Its design described in #891 is therefore **unverified
in this pass**, including background/locked-screen continuity and playback
barge-in. A transport-ready filesystem channel does not resolve that unknown.
The closed #901 roadmap is not a hidden prerequisite or a claimed implemented
human-input arbiter.

Refresh a source before adoption if its version, preview status, permission
model or local baseline changed. Record new evidence and revise the narrow
finding rather than converting a research citation into a feature promise.
