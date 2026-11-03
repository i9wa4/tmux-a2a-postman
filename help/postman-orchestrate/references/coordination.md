# Orchestration — from a request to an evidenced result — coordination

## 1. Work, updates and exact replies

A new node-addressed send creates a required reply. Reply to the actual POP'd
message ID with its generated command. A notification hint cannot select POP
or replace reading the full archive through EOF. Known positive unread counts
allow a finite POP/read batch; zero, empty or unknown stops it. No polling.

For a long-running request, preserve its original required reply for the final
result. A meaningful intermediate update can be a separate node-addressed
message and therefore has its own reply obligation. Keep original task,
correlation and outstanding responsibility in the existing work record. If an
ACK prematurely closed the transport exchange, record that fact and preserve
the unfinished task and explicit final route; transport closure is not task
completion. Never create ACK-of-ACK loops, quota-only sends, artificial status
requests or extra mail merely because an approval helper already answered.

Messenger-facing updates report a material implementation/test/review result
or concrete blocker, with evidence, responsible owner and next action. A quiet
turn is valid. Do not hide a failed child or unknown result behind a success
prefix, mailbox count or moving screen.
