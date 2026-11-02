---
name: postman-team-manager
license: MIT
description: |
  USE FOR: An explicit request to manage directly responsible Postman peers,
  investigate stalled replies, recover missing reports, or check whether
  current work still serves the caller's original objective.
  DO NOT USE FOR: Ordinary receipt/sending, configuration audits, restart
  recall, approval decisions, unsolicited monitoring, or managing every
  visible node merely because it appears in status.
---

# postman-team-manager

Use this procedure once per explicit management request or new relevant
reply. Scheduling belongs to the host; this skill starts no timer or daemon.

1. Establish the caller's original objective, completion criteria and your
   directly managed nodes from the supplied context. Communication topology
   does not assign management authority. Report missing context; do not
   invent an objective or assume fixed role names.
2. Run `tmux-a2a-postman get-status`. Select only those direct reports.
   Read the actual counts and saved timestamps using the
   [evidence and recovery procedure](references/management.md). This command
   neither captures panes nor refreshes observations. Null or stale evidence
   means uncertainty, not idle, blocked or complete.
3. Investigate a concrete outstanding obligation or suspicious wait using
   permitted evidence. Read your own relevant sent and claimed read archives
   completely, correlating exact message/reply references. Use
   [postman-usage](../postman-usage/SKILL.md) for genuine notifications, POP
   and complete reads. Never bypass POP by opening a peer's inbox.
4. If evidence cannot settle the question, send one targeted inquiry to the
   responsible direct report with
   [postman-send-message](../postman-send-message/SKILL.md). State the original
   objective, exact unresolved request, evidence, and the smallest question:
   active work and next milestone, a concrete blocker, or a missing result
   report. Request evidence rather than an acknowledgment alone.
5. Verify a genuine correlated reply or new evidence. Sending an inquiry is
   not recovery. Distinguish active work, blocked work, finished but unreported
   work, and unknown status. If the node already finished, ask it to finish
   its existing reporting/reply obligation; do not rerun or duplicate work.
6. Compare current actions with the supplied objective and criteria. Flag
   unrequested scope growth, repeated checks without new evidence, and work
   continuing after sufficient evidence. Ask for the smallest justified next
   step or blocker. Do not replace user-authorized scope with your preference.
7. Return an evidence-based result to the caller/parent: direct report,
   original obligation, observed facts and their freshness, inquiry/reply IDs
   when real, conclusion and uncertainty, and one concrete next action with
   its responsible party. Stop this invocation. An unresolved inquiry means
   remaining wait or escalation, never success. Await a genuine event or
   explicit new check; do not loop over status, POP or identical pings.

A closed Postman reply obligation proves transport correlation, not task
acceptance or completion. Completion needs the original criteria and actual
artifact/evidence checks. Stop further work when those are sufficient and
finish any reporting obligation. Respect approval and safety boundaries;
never fabricate replies, edit mailbox state, auto-reassign or duplicate tasks.

Each manager applies this same procedure to its own direct reports. Ask a
subteam's manager for a report rather than centrally monitoring its members.
No task tracker, repository, role hierarchy implementation or planning
framework is required.

The reference includes deterministic decision scenarios and a pure advisory
example. They check the stated decision policy offline; they do not establish
that a model will follow these instructions or authorize automated actions.
