# Design philosophy

Make the common path useful while keeping the whole system understandable,
operable and easy to update. Minimize total complexity, not line counts or the
number of Skills at the expense of capability or safety.

## 1. System-wide simplicity

1. **Compose and subtract before adding.** Reuse an existing command, resource
   or native agent capability first. Remove obsolete workarounds and consolidate
   duplicate instructions before introducing another mechanism.
2. **Give each responsibility one canonical owner.** Go owns deterministic
   transport, identity and approval guarantees. `postman.md` owns topology,
   authority and concise role contracts. Embedded HelpDocs own detailed command
   and workflow procedures; thin Skills own discovery and triggers. Optional
   consumers own local environment policy. Reference these owners instead of
   copying them, and revisit boundaries as implementations evolve.
3. **Count instructions and configuration as complexity.** Rules, prompts,
   exceptions, roles, cross-references, YAML, assets, dependencies, installation
   behavior, hidden defaults and update steps all cost understanding and
   maintenance, even when they are Markdown rather than Go.
4. **Mechanize demonstrated invariants.** Encode stable routing, safety and
   repeated avoidable judgments when actual failures or overhead justify it.
   Keep planning, review and context interpretation with the agent. Establish a
   concrete need before adding a state machine, scheduler, registry or watcher.
5. **Keep composition explicit.** Prefer inspectable inputs, outputs and
   version-matched assets. A custom configuration must replace an embedded
   default as a whole, without hidden merges. Do not introduce speculative
   plugin frameworks, global task databases, resident daemons, mandatory
   dependencies or cross-layer configuration.
6. **Preserve safety and provenance.** Unknown identity or authority must fail
   explicitly. Retain exact replies, outcome evidence, necessary tests and
   rollback capacity; simplify their implementation without weakening their
   guarantees. Report uncertainty instead of inferring success.
7. **Seek an offsetting simplification.** With each addition, ask what can be
   removed, consolidated or made optional. If complexity still increases,
   explain the concrete verified benefit. Arbitrary file-size or Skill-count
   limits are not substitutes for that judgment.

For review, ask: does the change increase or reduce total complexity across
code, configuration, prompts, HelpDocs, Skills, scripts, dependencies, state and
operational steps? What can an existing owner absorb, and what verified benefit
justifies the remainder? A short rationale in the existing issue or PR is
enough; no new template, metric or reporting system is required.

The
[CLI inventory](../../help/cli-spec.md#10-complete-interface-and-logging-inventory)
and [configuration contract](config-ssot.md) retain their detailed decisions.
Apply these principles to HelpDoc/Skill/default-team packaging and optional
launch presets as well as Go changes; do not duplicate their operating manuals.

## 2. Transport foundations

Favor readable files, exact identities, small foreground operations and honest
unknown outcomes. Durable delivery and notification acceptance are separate
facts. Do not infer task success from transport closure or screen changes.

Keep short workspace locks and exact reply/approval correlation. Remove dead
runtime paths and redundant policy matrices. Do not add compatibility wrappers,
background delivery workers or model-side polling to solve recipient activation.
See [architecture](file-first-architecture.md) for the operative contracts.
