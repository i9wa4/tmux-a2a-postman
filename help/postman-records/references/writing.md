# Local Markdown Artifacts — writing

## 1. Save-Point Usage Pattern

One of several recommended usage patterns alongside plans, research notes,
review packets, drafts, and scratch outputs: use a single local artifact as
a periodic save point during an in-progress task. Create it once at task
start, then update it in place — checklist, evidence log, decisions — as
work progresses, so state stays resumable without replaying chat or message
history. This is the same canonical-file rule as
[Canonical Path Selection](selection.md): reuse one file
across the task rather than creating a new snapshot per update.

Keep the local artifact canonical and current, including after preparing or
sharing a human-facing copy. Delivery does not replace the task record.

## 2. Suggested Artifact Shape

Keep artifacts compact but resumable:

```markdown
# <Task Title>

## Task

One paragraph describing the requested outcome.

## Original Checklist

- [ ] Required item from user or orchestrator.

## Plan And Progress

- [ ] Milestone or active work item.

## Evidence Log

- YYYY-MM-DD HH:MM TZ: Read files or skills.
- YYYY-MM-DD HH:MM TZ: Ran command and result.

## Decisions

- Decision and reason.

## Surprises And Discoveries

- Unexpected finding and impact.

## Verification

- Command/result or inspection evidence.

## Blockers

- None, or exact blocker.

## Completion Verdict

- Original checklist: PASS/BLOCKED.
```

For resume, record the current branch, dirty/clean status, last completed
action, next action, blockers, and validation still required.

## 3. Human-Facing Markdown

Make the document concise, scannable, and sufficiently explanatory. Lead with
the main conclusion, then include necessary context, evidence, and action
items. Preserve critical reasoning, constraints, and findings; adjust length
to the request and task complexity without fixed word or line limits.

Keep detailed working history in the canonical local artifact when needed.
A delivery copy can summarize that history rather than reproduce raw
operational logs or every working note verbatim. Do not proactively create
or propose a Gist merely because the requester wants readable Markdown.

Use Mermaid when a diagram materially improves understanding. Large
end-to-end diagrams are welcome for architecture or workflow context; add
smaller, focused diagrams of important parts when those close-ups help,
especially on a phone. There is no node-count limit or required number of
diagrams. Detailed authoring belongs to the separate `diagramming` skill.

GitHub's web experience can render Mermaid; do not assume GitHub Mobile can.
Keep important relationships, findings, and actions understandable from the
surrounding prose and labels even when a diagram does not render.
