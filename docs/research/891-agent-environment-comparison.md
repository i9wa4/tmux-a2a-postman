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

The maintainer wants a Unix-style foundation for multi-agent orchestration:
small tools with clear responsibilities, explicit readable inputs/outputs and
durable evidence that other tools can inspect. Postman supplies reliable mail,
notification and exact execution decisions; an environment-owned work record
can retain objectives and evidence; Skills compose these into delegation,
resumption, review and acceptance procedures.

Extract useful experience from dotfiles into reusable workflow guidance rather
than importing its launchers, fixed role names, storage implementation or test
infrastructure as product dependencies. Prefer Skill-level composition for
judgment and workflow. When evidence identifies a mechanically enforceable
invariant that tools must guarantee, improve Postman at that narrow boundary.
This direction permits substantial core improvements when justified; it is not
an instruction to add orchestration policy to transport by default.

The aim is a foundation that can approach and exceed frontier systems as tools,
models and practices improve. Judge that through accepted outcomes, recovery,
human attention and total resource/maintenance cost, not agent count or a
feature checklist. The work-state comparison below is one proposed step;
this PR implements neither its Skill nor a work-state subsystem.

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
| [CLI contract](../cli-spec.md), [file-first architecture](../design/file-first-architecture.md), [#937 acceptance audit](../validation/937-acceptance.md)                             | Durable mail, exact claims/replies, native ownership, bounded foreground notification and saved observations have explicit contracts and deterministic process checks. **High.** | Saved screen movement, POP and reply correlation cannot prove work progressed or met acceptance criteria.                                        |
| `internal/workspace/execution_test.go`: `TestExecutorCancellationAndLateReplyCannotAuthorizeNewCall`, `TestExecutorEqualConcurrentInvocationsHaveIndependentApprovals`                | An old execution decision cannot authorize a new invocation; equal concurrent requests have separate approval correlation. **High.**                                             | This is invocation authority, not a general logical-task/attempt lifecycle or duplicate-side-effect recovery guarantee.                          |
| [External-channel contract](../design/issue-767-external-channel-contract.md); `external_test.go` crash boundaries, superseded scopes, binding isolation and revised-decision closure | Existing mail mechanisms support bounded human exchanges, exact pending decisions and catch-up after interrupted publication. **High.**                                          | Local filesystem identity does not authenticate a remote human; no voice bridge, scheduler or generic cancellation protocol follows from this.   |
| [Team-manager Skill](../../skills/postman-team-manager/SKILL.md), its offline decision scenarios, recall and usage Skills                                                             | Explicit management can inspect direct reports, retain uncertainty, ask one targeted question and require actual evidence. **High** for authored policy and offline checks.      | Effective model adherence and reduced supervision remain **unmeasured**. No automatic monitoring is supplied.                                    |
| [Recursive-session trial](../recursive-sessions-trial-runbook.md), deterministic terminal-agent suites                                                                                | Real tmux input drives receiver-owned POP, full archive reads and exact return messages across direct parent/child interfaces. **High.**                                         | These authored deterministic behaviors do not demonstrate autonomous decomposition, semantic preservation or reviewer quality.                   |
| Pinned dotfiles `postman.md`, guardian/critic policy, Logbook/artifact conventions and Nix skill installation                                                                         | Goals, review routing, evidence and durable work notes already have owners outside transport. **High** for source policy.                                                        | An instruction or successful fixture is not proof that deployed agents follow it; cross-runtime permission equivalence needs its own evaluation. |

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

| Question                                    | External evidence and narrow finding                                                                                                                                                                                                                                    | Local comparison and classification                                                                                                                                                                                                                               | Decision, experiment and owner                                                                                                                                                                                                                                    |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Q1. Work surviving replacement**          | Orca separates dispatch authority from task state and guards terminal transitions; Symphony reconciles tracker work; Managed Agents separates durable events, harness and sandbox. [S1–S3] **High** for inspected Orca/Symphony text; **Medium** for managed operation. | Mail/history and exact execution decisions survive defined failures. No local test establishes logical-task retry ownership against an old worker's late DONE. **Unmeasured capability** in artifacts/runtime; generic task-attempt control is **another layer**. | Compare recall/history with a referenced canonical work record in E1. Existing Logbook is a concrete optional backend, not a transport requirement. Runtime/workflow ownership stays outside core; propose an invariant only for a reproduced protocol violation. |
| **Q2. Reasoning versus executable control** | Workflow Runs executes model-produced workflow code; Agent Framework offers explicit graphs and human gates. A completed workflow can include handled child failures; Go preview lacks some other-language workflow features. [S4, S10] **Medium.**                     | Fixed mail/approval transitions are deterministic; decomposition and review decisions remain Skills. No comparison measures coordinator turns or recovery complexity. **Unmeasured**, with a **deliberate** small-core boundary.                                  | Compare a short script with an existing Skill for one repeated fan-out/join in E2. Workflow owner may compose an external runner if measured benefit exceeds setup cost; do not create a Postman workflow language.                                               |
| **Q3. Useful throughput from teams**        | Cursor Projects separates coordinator and workers; Gas Town names project/worker/integration roles. Research finds topology gains vary with task, prompts and budget. [S7, S9, S11–S13] **Medium** for reported outcomes; **High** for inspected Gas Town README.       | Recursive routing and common role templates exist. Accepted output per resource and configuration growth per added team have not been measured. **Unmeasured**, not a proven need for dynamic staffing or global discovery.                                       | E3 compares single, small centralized and recursive presets under matched budgets. Dotfiles/workflow owner measures prompt duplication, setup and handoffs; reject extra layers if accepted throughput falls.                                                     |
| **Q4. Independently judgeable results**     | Outcomes grades against a rubric using separate context; cloud-agent/harness reports provide inspectable runtime artifacts. [S5, S8, S20] **Medium.**                                                                                                                   | Guardian/critic and artifact contracts exist; transport completion remains distinct from original-task acceptance. Human time and correlated review misses are **unmeasured**. Evidence presentation belongs to **Skills/runtime**.                               | E4 blinds review to the implementer's justification initially and checks actual combined artifacts. Review-policy owner retains dissent and measures false completion and review time; no core verdict ledger.                                                    |
| **Q5. One conversation controlling work**   | GPT-Live handles simultaneous conversation/backend work; the application owns permissions, progress and whether interruptions cancel work. A2UI describes interface actions, not backend authority. [S15, S23–S24] **Medium.**                                          | #767 has stable scoped pending choices, while general pause/reprioritize/cancel is absent. This is a **proven interface gap** and **another layer's responsibility**, not missing mail transport. Voice maturity is **unknown** here.                             | E5 first uses deterministic text events against one pending action, including delayed old approval and reconnect. Human-interface owner decides playback interruption versus dispatch pause versus task cancellation; only then assess Japanese audio.            |
| **Q6. Experience becoming improvements**    | Dreaming is a research preview producing a separate curated memory output; vendor reports describe refining harnesses/rules. [S2, S6–S7] **Medium.**                                                                                                                    | Human-invoked research, durable incident notes, Skills and regression tests already provide an adoption path. Benefit on later work is **unmeasured**; automatic rule rewriting is a rejected **trade-off**.                                                      | E6 verifies an injected failure, proposes one reviewed change and checks a held-out case plus a negative control. Skill owner adopts a versioned diff with rollback only after maintainer review.                                                                 |
| **Q7. Governable resources and authority**  | Paperclip documents budget stops and incomplete accounting; managed runtimes separate credentials and sandbox execution. [S3, S14, S22] **Medium.**                                                                                                                     | Exact reviewed execution is not an OS boundary; Postman does not collect provider billing or enforce an organization budget. **Another layer**, with an explicit **core trade-off**. Current cross-provider cost coverage is **unmeasured**.                      | E7 uses synthetic priced/unpriced events and native runtime limits; pause new dispatch while preserving work. Runtime/provider owner proves actual stop behavior and uncertainty handling before claiming a spend cap.                                            |

## 5. Ten lenses applied to the same investigation

These refine the seven questions; they are not ten new implementation issues.
Every experiment is **proposed and not run** in this research increment.

| Lens                                                            | Evidence, local implication and confidence                                                                                                                                                                                                                                         | Smallest experiment and decision owner                                                                                                                                                                                           |
| --------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **C1. Task-adaptive topology** (Q2/Q3/Q7)                       | Agensh's large-agent results concern specific program tests; MASS emphasizes a pruned design space and prompts; the scaling study finds task-dependent gains and losses. [S11–S13] **Medium.** Local transport checks do not measure organization efficiency.                      | E3: pair a sequential task with a separable one; compare accepted outcomes at fixed total calls/time, not worker count. Workflow owner retains the simplest successful preset.                                                   |
| **C2. Meaning-preserving handoff** (Q2/Q3)                      | Routed Graph Handoff reports both benefits and graph-only regressions. [S16] **Medium.** Exact reply IDs establish correlation, not retention of exclusions, uncertainty or original goals.                                                                                        | E2: carry known requirements across three hops using prose, structured Markdown and a hybrid; grade receiver actions and omitted constraints. Skill owner changes only the handoff convention if supported.                      |
| **C3. Independent reasoning/global objectives** (Q3/Q4/Q7)      | PoTRE varies reasoning methods; the AI-organizations study highlights organizational objective/ethical failures. [S17–S18] **Medium.** Different role or provider names alone do not prove independent review.                                                                     | E4: independent-first versus justification-first review of seeded local and global defects; record correlated misses and discarded dissent. Guardian/critic policy owner decides the review order.                               |
| **C4. Sufficient, current, sourced context** (Q1/Q3/Q6)         | Agentic RAG tests context sufficiency; inspected Graphiti edges retain episodes and validity/reference times. [S19, S25] **Medium/High**, respectively. These fields do not prove true extraction. Local Markdown may already suffice.                                             | E6: provide an old rule, explicit newer authority and one missing fact. Require correct current rule, provenance and an explicit unknown. Skill owner refines references before considering retrieval infrastructure.            |
| **C5. Verified failure attribution** (Q4/Q6)                    | ErrorProbe verifies causal hypotheses with probes; MP-Bench studies ambiguous multi-perspective attribution. [S26–S27] **Medium.** Current mail/capture/audit evidence cannot by itself identify a reasoning cause. #973's candidate traces help correlation, not causal proof.    | E6: inject one runtime fault and one delegation omission; require a distinguishing probe. Diagnostic owner leaves ambiguous cases unknown and never executes archived commands merely because they appear in evidence.           |
| **C6. Parallel integration and executable evidence** (Q1/Q3/Q4) | GitButler parallel branches share a working directory; the harness report uses inspectable per-worktree runtime evidence. [S20–S21] **Medium.** Separate commits and conflict-free merges do not prove combined behavior.                                                          | E4: change opposite sides of a mock API in separate worktrees, then test the integrated result. Integration owner records semantic defects, setup/merge time and owned runtime teardown; shared files are not runtime isolation. |
| **C7. Complete non-coding workflows** (Q3/Q4/Q7)                | Ironclad evaluates business-workflow configuration using multiple criteria; rubric averages and estimated duration are not full-task success or observed customer savings. [S28] **Medium.** Local evidence is predominantly coding/transport.                                     | E7: simulate threshold/exception/human-approval cases in a disposable application and inspect final state. Domain owner defines acceptance before running it; no live business action.                                           |
| **C8. Delegation/memory trust** (Q3/Q6/Q7)                      | Containment engineering separates low-trust content, tool authority and runtime enforcement. [S22] **Medium.** Authentic mail or an agent summary does not grant human consent or factual truth.                                                                                   | E2/E6: relay a harmless instruction-like string from a low-trust document; require it to remain quoted data and exclude it from standing rules. Policy/runtime owners enforce actual permissions outside prompts.                |
| **C9. Stable voice/UI actions** (Q5)                            | GPT-Live separates speech from backend control; A2UI current v0.9.1 and candidate v1.0 must not be conflated; the voice guide grades resulting application state. [S15, S23–S24] **Medium.** #767 already supplies useful exact decision scope, not voice or generic cancellation. | E5: optional view and text/voice adapters refer to one current pending action and scope; test revision, delay, duplicate answer and reconnect. Interface owner rejects stale actions and checks the final state.                 |
| **C10. Repeated reliability/fair measurement** (all Qs)         | Evaluation guidance distinguishes any-success `pass@k` from all-success `pass^k`; infrastructure changes can alter scores. [S29–S30] **Medium.** Deterministic fixtures and model capability trials answer different questions.                                                    | Apply the common protocol below to E1–E7. Evaluation owner reports every trial, environment, resource budget and uncertain grader result; no confidence from a single successful rally.                                          |

## 6. Bounded experiments and decision gates

Owners here name existing **responsibilities**, not new agents or services.
The maintainer chooses a person/session for an explicitly invoked experiment.
These plans authorize neither paid provider trials nor live-fleet actions.

| ID / first useful outcome                      | Setup, controls and falsifiable checks                                                                                                                                                                                                                                                                                                                                       | Initial decision and stop condition                                                                                                                                                                                                                                                   |
| ---------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **E1 — restart without false completion**      | Run the shared mail-only versus referenced-record comparison below in an isolated two-team workload. Stop attempt A after an observable effect, start B, then deliver A's late DONE/failure and execution approval; include a no-crash control in each arm. Check original criteria, current owner/attempt, exact evidence revision, unresolved effects and execution count. | Runtime/workflow owner first reuses recall and artifacts. Fail if old evidence accepts B, changes current authority or repeats an uncertain side effect. Propose a core change only for a separately reproduced mail/approval invariant violation.                                    |
| **E2 — preserve constraints through handoffs** | Cross three levels with known exclusions, uncertainty and low-trust quoted text. Compare mail-only and referenced-record arms for each prose/structured/hybrid handoff. Keep underlying facts and permissions equal. Verify transport first; a separate capability study must grade receiver behavior and retained original constraints.                                     | Skill owner measures omitted/changed constraints and unauthorized actions. Stop on any authority promotion; reject structure that compresses text but worsens behavior. Do not mistake a scripted fake's correct answer for semantic evaluation.                                      |
| **E3 — choose the smallest effective team**    | Use one sequential and one separable task; single-agent control versus two small declared presets. Match total model budget, wall-time allowance, model/version and acceptance rubric; count setup lines, duplicated policy and coordinator calls.                                                                                                                           | Dotfiles/workflow owner retains a preset only if accepted throughput improves without more false completion or supervision. Do not choose a winner if budgets or tasks differ.                                                                                                        |
| **E4 — accept integrated evidence quickly**    | Seed local and global defects, stale DONE reports and an incorrect completion claim. Compare mail-only and referenced-record arms within each review order; inspect integrated behavior and original acceptance criteria. Blind expected defects and measure whether humans can judge correctly without reopening the entire conversation.                                   | Review/integration owner records false acceptance, dissent, review minutes and reproducibility. Keep existing guardian/critic/artifact conventions unless the alternative wins on outcomes and attention.                                                                             |
| **E5 — control the correct pending action**    | Use a disposable backend and #767's current binding/exchange/scope. Revise action X, delay its old answer, interrupt playback, request pause/cancel, disconnect/reconnect and repeat an answer. Compare with uninterrupted text control.                                                                                                                                     | Interface owner checks actual action count/current state and explicit cancellation acknowledgment. Stop if old approval affects revised work. Audio/model evaluation follows only after deterministic authority tests, with separately authorized Japanese recordings/provider usage. |
| **E6 — improve from verified incidents**       | Inject known runtime/delegation/stale-context failures. Require a probe distinguishing causes; propose one versioned Skill/tool/test diff. Evaluate a held-out failure and a valid negative control without the original diagnosis in the evaluated agent's context.                                                                                                         | Skill/diagnostic owner adopts only a reviewed, rollbackable change supported by the held-out outcome. Leave causality unknown when evidence cannot distinguish it; reject automatic memory/policy replacement.                                                                        |
| **E7 — bounded non-coding execution**          | Simulate an approval threshold, exception and human decision; add priced, unpriced and delayed accounting events with a small concurrency limit. Pause new dispatch, retain ownership, resume once and verify final application state.                                                                                                                                       | Domain/runtime owner checks no unknown cost becomes zero, no duplicate effects, and no action exceeds authorization. Runtime stop and billing bounds need backend evidence; a budget badge or prompt is insufficient.                                                                 |

### 6.1. Shared work-state comparison for E1, E2 and E4

The [maintainer's review comment][work-state-comment] proposes a hypothesis:
a portable convention for using a durable work record may improve recovery,
handoffs and acceptance beyond what mail correlation alone provides. This is
**unmeasured**, not a request to implement a ledger or a new Skill in this PR.

[work-state-comment]: https://github.com/i9wa4/tmux-a2a-postman/pull/974#issuecomment-6568131376

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

**Recommended order:** E1/E2 establish durable authority and preserved meaning;
E4/E6 establish judgeable outcomes and evidence-based improvement; E3 then
measures organization cost. E5/E7 remain separately owned interface/domain
experiments. Reuse existing mechanisms first. A future issue needs an observed
failure, smallest proposed remedy, owner and acceptance check; no automatic
seven- or ten-issue implementation backlog follows from this report.

## 7. Primary-source registry

All mutable web documents below were retrieved on **2026-11-02**. Dates in
titles describe publications, not a claim that a deployed service stayed
unchanged. Paper results were read, not reproduced locally. For pinned GitHub
files, the revision shown is the **commit**, not the returned file/blob hash.

| Source                                                                                                                                                                                                     | Version / evidence                                                                  | Narrow finding and confidence                                                                                                                                                                                                        |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **S1** [Orca dispatch completion](https://github.com/stablyai/orca/blob/f307391637b53a0e05fe357f4942ac2718ada767/src/main/runtime/orchestration/db/dispatch-context/dispatch-completion.ts)                | Source inspected, commit `f307391637b53a0e05fe357f4942ac2718ada767`                 | Terminal transitions revoke dispatch capability; heartbeat/failure transitions are guarded. **High** for this code. Public orchestration documentation was inaccessible in this pass; broader behavior is unknown.                   |
| **S2** [Symphony engineering report](https://openai.com/index/open-source-codex-orchestration-symphony/), [SPEC](https://github.com/openai/symphony/blob/be10a1b79df723d6d7612b5651c8522704dafb2e/SPEC.md) | Report 2026-04-27; inspected Draft v1 at `be10a1b79df723d6d7612b5651c8522704dafb2e` | Issue-driven workspaces and reconciliation; exact in-memory orchestration state need not persist. **High** for specification, **Medium** for reported operational benefits; no reproduced throughput claim.                          |
| **S3** [Managed Agents architecture](https://www.anthropic.com/research/managed-agents)                                                                                                                    | Vendor architecture report; original engineering URL redirects here                 | Session events, harness and sandbox are separate interfaces. **Medium.** Replacing compute and restoring authoritative work state are distinct concerns.                                                                             |
| **S4** [Workflow Runs](https://platform.claude.com/docs/en/managed-agents/workflow-runs)                                                                                                                   | Mutable product documentation                                                       | Workflow code coordinates agents; handled child failures can coexist with a completed workflow. **Medium.** Runtime completion must not be equated with all task outcomes succeeding.                                                |
| **S5** [Outcomes](https://platform.claude.com/docs/en/managed-agents/define-outcomes)                                                                                                                      | Mutable product documentation                                                       | Separate-context rubric evaluation is an available mechanism. **Medium** for documentation, **Low** for its effectiveness on this environment's work.                                                                                |
| **S6** [Dreaming](https://platform.claude.com/docs/en/managed-agents/dreams)                                                                                                                               | Research preview, feature header `dreaming-2026-04-21`                              | A separate curated memory output can be reviewed before adoption. **Medium.** This does not justify silent mutation of live operating rules.                                                                                         |
| **S7** [Cursor Projects](https://cursor.com/blog/projects)                                                                                                                                                 | Vendor report, 2026-09-10                                                           | Coordinator/worker separation and shared project context. **Medium.** Scale and productivity statements are vendor reports, not controlled local measurements.                                                                       |
| **S8** [Cursor cloud computer use](https://cursor.com/blog/agent-computer-use)                                                                                                                             | Vendor report, 2026-02                                                              | Inspectable computers and visual evidence support review. **Medium.** No local trial of VM recovery or human review savings.                                                                                                         |
| **S9** [Gas Town README](https://github.com/gastownhall/gastown/blob/466aa41c83ee73b1fa0eb79eb9d172fb1eca9476/README.md)                                                                                   | Source documentation inspected at `466aa41c83ee73b1fa0eb79eb9d172fb1eca9476`        | Git-backed work, town/rig roles, monitoring and merge services are documented; normal startup includes long-lived services. **High** for the README, not demonstrated reliability.                                                   |
| **S10** [Microsoft Agent Framework overview](https://learn.microsoft.com/en-us/agent-framework/overview/)                                                                                                  | Current product/library documentation                                               | Explicit workflows coexist with agent reasoning. Go 1.26 support is preview with language-specific limitations. **Medium**; do not infer library capability parity across languages.                                                 |
| **S11** [Agensh](https://arxiv.org/abs/2609.26781v1)                                                                                                                                                       | Paper v1, 2026-09                                                                   | Large-scale shared-work coordination is a research result on specific tasks. **Medium** for reported findings, **Low** for local transfer; test-pass fractions are not full-task acceptance or cost-normalized gains.                |
| **S12** [MASS](https://research.google/pubs/multi-agent-design-optimizing-agents-with-better-prompts-and-topologies/)                                                                                      | ICLR 2026 publication/abstract                                                      | Prompt and topology search can favor simpler pruned designs. **Medium** for reported results; no universal preferred topology follows.                                                                                               |
| **S13** [Scaling Agent Systems](https://arxiv.org/abs/2512.08296v3)                                                                                                                                        | Paper v3                                                                            | Matched-budget architectural comparisons show task-dependent benefits and regressions. **Medium.** Its settings and scale differ from Agensh.                                                                                        |
| **S14** [Paperclip costs/budgets](https://docs.paperclip.ing/guides/day-to-day/costs/)                                                                                                                     | Mutable docs; explicitly distinguishes v2026.1023.0 warning UI from a later fix     | Budget controls pause new work and retain assignments; incomplete/unpriced accounting remains explicit. **Medium.** In-flight overshoot and backend enforcement were not tested; an absolute no-overspend claim is unsupported here. |
| **S15** [GPT-Live guide](https://developers.openai.com/api/docs/guides/live)                                                                                                                               | Mutable API documentation                                                           | Client delegation permits an independent backend; the application owns permission, progress and cancellation. **Medium.** Full-duplex speech does not itself cancel backend work.                                                    |
| **S16** [Routed Graph Handoff](https://arxiv.org/abs/2608.25277v1)                                                                                                                                         | Paper v1                                                                            | Handoff representation benefits vary by task; graph-only delegation also regresses in a reported setting. **Medium**, not demonstrated local semantic preservation.                                                                  |
| **S17** [PoTRE](https://research.google/pubs/potre-test-time-reasoning-inspired-by-cognitive-heterogeneity/)                                                                                               | TMLR 2026 publication/abstract                                                      | Different reasoning procedures and aggregation are studied. **Medium.** Role/vendor diversity alone is not the tested mechanism.                                                                                                     |
| **S18** [AI organizations](https://alignment.anthropic.com/2026/ai-organizations/)                                                                                                                         | Research report, linked paper `2604.10290`                                          | Simulated organizations expose global-objective and ethical failures. **Medium** for reported observations, **Low** for prevalence in real teams.                                                                                    |
| **S19** [Agentic RAG](https://research.google/blog/unlocking-dependable-responses-with-gemini-enterprise-agent-platforms-agentic-rag/)                                                                     | Vendor research/product report                                                      | Context sufficiency can guide further retrieval. **Medium.** Relevance, truth and authorization remain separate checks.                                                                                                              |
| **S20** [Harness engineering](https://openai.com/index/harness-engineering/)                                                                                                                               | Vendor engineering report                                                           | Per-worktree runtime observability and executable evidence improve inspectability. **Medium** for the case study, not a controlled productivity result.                                                                              |
| **S21** [GitButler parallel agents](https://docs.gitbutler.com/ai-agents/parallel-agents)                                                                                                                  | Product documentation                                                               | Parallel branches share working files/runtime; separate worktrees offer different isolation. **Medium.** Version-control separation is not independent execution.                                                                    |
| **S22** [Containment engineering](https://www.anthropic.com/research/how-we-contain-claude)                                                                                                                | Vendor report; original engineering URL redirects here                              | Runtime boundaries, credential separation and treatment of untrusted content matter across agents. **Medium.** Trusted connector invocation does not make its contents authoritative.                                                |
| **S23** [A2UI](https://a2ui.org/)                                                                                                                                                                          | v0.9.1 current; v1.0 candidate, formerly v0.10 draft                                | Declarative client rendering; candidate v1.0 adds action IDs/action responses. **Medium.** Interface structure grants no backend permission, and candidate features are not current-release guarantees.                              |
| **S24** [GPT-Live evaluation guide](https://developers.openai.com/cookbook/examples/audio/voice_agent_evaluation)                                                                                          | Mutable official evaluation guide                                                   | Grades corrections, authorization and final application state through progressively realistic audio/conversation. **Medium.** No Japanese/latency result was reproduced here.                                                        |
| **S25** [Graphiti EntityEdge](https://github.com/getzep/graphiti/blob/a9ef13f7e2cce23e92abd2f76947f42e270da25a/graphiti_core/edges.py)                                                                     | Source inspected at `a9ef13f7e2cce23e92abd2f76947f42e270da25a`                      | `episodes`, `valid_at`, `invalid_at`, `expired_at` and `reference_time` represent provenance/temporal distinctions. **High** for fields, not extraction accuracy. The inspected issue pin is not claimed to be current HEAD.         |
| **S26** [ErrorProbe](https://arxiv.org/html/2604.17658v1)                                                                                                                                                  | Paper v1                                                                            | Backward attribution and executable probes ground retained diagnostic patterns. **Medium** for research findings; replay still requires authorization.                                                                               |
| **S27** [MP-Bench](https://arxiv.org/abs/2603.25001)                                                                                                                                                       | Paper, retrieved snapshot                                                           | Multiple perspectives and plausible causes complicate failure attribution. **Medium.** A persuasive single-cause narrative is not ground truth.                                                                                      |
| **S28** [OpenAI × Ironclad](https://openai.com/index/advancing-computer-use-with-ironclad/)                                                                                                                | Vendor/model evaluation, 2026-10-06                                                 | Multi-criterion workflow assessment broadens the domain beyond coding. **Medium.** Mean rubric scores and simulated duration estimates are not autonomous-task success or observed customer time savings.                            |
| **S29** [Agent evaluation](https://www.anthropic.com/research/demystifying-evals-for-ai-agents)                                                                                                            | Official guidance; engineering URL redirects here                                   | Any-success and all-success summaries answer different reliability questions; evaluate actual outcomes. **Medium.** Grader calibration remains necessary.                                                                            |
| **S30** [Infrastructure noise](https://www.anthropic.com/research/infrastructure-noise)                                                                                                                    | Vendor experiment; engineering URL redirects here                                   | Resource enforcement can change measured performance. **Medium.** Its reported score difference is not a universal correction factor.                                                                                                |

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
