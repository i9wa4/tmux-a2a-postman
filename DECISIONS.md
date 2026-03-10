# Architecture Decision Log

## A1: guardian->messenger direct edge

- **Date**: 2026-03-10
- **Status**: Accepted
- **Context**: Guardian needs to alert the user (via messenger/ui_node) about quality
  issues. Currently guardian has no edge to messenger and must route through orchestrator.
- **Decision**: Add a direct `guardian -- messenger` edge.
- **Rationale**: Guardian is a read-only alerter with no implementation authority.
  The messenger contact is for TUI alerts only. No privilege escalation occurs because
  guardian cannot instruct messenger to take actions — it can only surface information.
- **Alternatives rejected**: Orchestrator-mediated routing — adds unnecessary hop for
  time-sensitive TUI alerts.
