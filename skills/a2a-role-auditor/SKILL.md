---
name: a2a-role-auditor
description: |
  Role template auditor for tmux-a2a-postman multi-agent systems.
  Audits nodes/*.toml role definitions to fix node-to-node interaction breakdowns.
  Use when:
  - A node behaves unexpectedly (routes wrongly, ignores messages, approves nothing)
  - Nodes can't see each other in talks_to_line (after confirming session/PING issue is ruled out)
  - Adding a new node and need to verify its template is complete and consistent
  - User wants to review or improve role definitions for any node
  Do NOT use for daemon-level failures (dead-letter from routing/edge misconfiguration);
  run triage first to determine if the issue is template-level.
---

# a2a-role-auditor

Audits `nodes/*.toml` role templates in a tmux-a2a-postman project to fix
node-to-node interaction breakdowns.

## 1. Mandatory Triage Gate

Before running any template audit, determine whether the issue is daemon-level
or template-level.

**Daemon-level indicators** (stop here — report as config issue, do NOT produce
patches):

- Wrong or missing edges in `postman.toml`
- `nodes/{node}.toml` file does not exist
- Session disabled in postman TUI

**Template-level confirmed** (node exists, edges correct, but behavior is
wrong):

- Proceed to the 11-check audit below.

## 2. 11-Check Audit

### 2.1. Pre-check: File Existence (binary)

For every node referenced in `postman.toml` edges, verify `nodes/{node}.toml`
exists.

- PASS: file present
- FAIL: file missing → emit BLOCKING finding; abort all further checks for that
  node

### 2.2. Check 1 — Routing Clarity

- PASS: template names at least one recipient for output messages
- FAIL: template says "send a message" without specifying who receives it

### 2.3. Check 2 — Completion Protocol

- PASS: template specifies a machine-readable signal word (e.g., APPROVED, DONE,
  BLOCKED) for task completion
- FAIL: completion state is undefined or described only in natural language

### 2.4. Check 3 — Fallback Routing

- PASS: template names an alternative recipient when the primary contact is
  absent from `talks_to_line`, AND the fallback recipient has an actual edge in
  `postman.toml`
- FAIL (no fallback): no fallback specified
- FAIL (unreachable fallback): template specifies a fallback to a node that has
  no edge connecting it to this node in `postman.toml` — the fallback is
  unreachable

### 2.5. Check 4 — Cross-Edge Consistency

Two sub-checks:

- **Binary**: does the template mention only nodes that exist as edges in
  `postman.toml`? (PASS/FAIL — no judgment)
- **Judgment**: are the described routing semantics consistent with edge
  direction? (LLM assessment — label findings with `Type: JUDGMENT-BASED`)

### 2.6. Check 5 — on_join Completeness

- PASS: `on_join` field is non-empty
- FAIL: `on_join = ""`

### 2.7. Check 6 — Messaging Protocol Instructions

- PASS: template contains instruction to use `create-draft` CLI command for
  drafting messages (e.g., "tmux-a2a-postman -- create-draft")
- PASS (also verify): template does NOT instruct the agent to use
  `mv draft/ post/`; agents must use `tmux-a2a-postman send <filename>` to
  submit drafts
- PASS (also verify): template does NOT instruct the agent to use `mv` to move
  files to `read/`; agents must use `tmux-a2a-postman archive <filename>` to
  mark messages as read
- FAIL: template lacks create-draft protocol instruction — agents may manually
  create files in draft/, causing malformed envelope metadata
- FAIL: template instructs `mv draft/ post/` — deprecated; use
  `tmux-a2a-postman send <filename>`
- FAIL: template instructs `mv inbox/... read/` or equivalent — deprecated; use
  `tmux-a2a-postman archive <filename>`

**Diplomat sub-check** (applies only when `diplomat_node` is set in
`postman.toml`):

- PASS: template for the diplomat node documents
  `--cross-context <contextID>:<node>` syntax when cross-context messaging is
  part of its responsibilities
- FAIL: diplomat node template has no mention of `--cross-context` — agents
  cannot discover the cross-context delivery path from the template alone (#164:
  `create-draft --cross-context` is the canonical cross-context primitive)

### 2.8. Check 7 — Pre-Approval Verification

Applies only to nodes whose template contains APPROVED or REJECTED signal words
(typically reviewer or approver nodes).

- PASS: template contains an explicit verification step before issuing verdict
  (e.g., "verify artifact exists with git status")
- FAIL: template issues APPROVED/REJECTED without requiring artifact
  verification — approvals based on plan text alone are unreliable

### 2.9. Check 8 — draft_template Disclaimer

Applies only to nodes that define a `draft_template` field.

- PASS: `draft_template` includes a disclaimer such as "(for context only — only
  nodes in 'You can only talk to:' are reachable)"
- FAIL: `draft_template` is present but lacks a reachability disclaimer — agents
  may assume all nodes listed in the template are contactable, leading to
  dead-lettered messages

### 2.10. Check 9 — Dropped Ball Timeout Configured

Applies to all non-observer nodes (nodes whose role does NOT contain
"observer").

- PASS: `dropped_ball_timeout_seconds` is greater than 0
- FAIL: `dropped_ball_timeout_seconds` is 0 or absent — the node can hold the
  ball indefinitely without triggering a dropped-ball alert, causing silent
  stalls

### 2.11. Check B-I8 — Protocol Reminder Presence

- PASS: template references the postman protocol (e.g., contains
  "tmux-a2a-postman --help", "protocol", "tmux-a2a-postman", or "create-draft")
- FAIL: template lacks any protocol reminder — agents may ignore messaging
  conventions, leading to malformed messages or manual file creation

### 2.12. Check B-I9 — on_join Help Reference

- PASS: `on_join` field references the help command (e.g., contains
  "tmux-a2a-postman -- help")
- FAIL: `on_join` does not reference the help command — agents miss the
  self-service protocol docs and command reference available via CLI

## 3. Findings Format

Every finding MUST use this exact schema:

```text
[SEVERITY] Node: {node}
Field: nodes/{node}.toml:[{node}].{field}
Check: {check name}
[Type: JUDGMENT-BASED]   <- optional, only when applicable
Result: FAIL
Issue: {description}
Fix:
  {exact replacement text}
```

Severity: `BLOCKING` | `IMPORTANT` | `MINOR`

`Type: JUDGMENT-BASED` is a separate flag, not a severity level.
Present findings in order: BLOCKING first, then IMPORTANT, then MINOR.

## 4. Configuration File Paths

All files are read from the user's XDG config directory:

| File                             | Path                                                        |
| -------------------------------- | ----------------------------------------------------------- |
| Main config (edges + node sections) | `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml`        |
| Node template files              | `$XDG_CONFIG_HOME/tmux-a2a-postman/nodes/{node}.toml`      |
| Default config reference         | `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.default.toml`   |

- `$XDG_CONFIG_HOME` defaults to `~/.config` if unset.
- `postman.toml` defines both `[[edges]]` (routing) and `[node-name]` sections
  (per-node config).
- `nodes/{node}.toml` holds the role template (`template`, `on_join`,
  `draft_template`, etc.) for each node.
- `postman.default.toml` is a canonical reference listing all configurable
  values with their defaults and comments. Consult it when auditing
  `dropped_ball_timeout_seconds` defaults and other per-node configurable fields
  (#249 policy: all values must appear explicitly here).

## 5. Workflow

1. Read `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml` — extract edges, build
   adjacency map
2. Read each `$XDG_CONFIG_HOME/tmux-a2a-postman/nodes/{node}.toml` (source of
   truth; runtime session templates are NOT compared)
3. For each node: run Pre-check, then Checks 1–9, B-I8, and B-I9 in order
4. Produce findings report sorted by severity
5. Propose concrete patch text for every finding
6. Present to user for feedback; iterate until approved

NOTE: Do NOT auto-apply patches. Propose only; the user applies manually or
delegates to a worker node.

### 5.1. Nix Store Warning

Before attempting to patch any node file, check if the deployed path is
read-only:

```sh
ls -la $XDG_CONFIG_HOME/tmux-a2a-postman/nodes/
```

If files are owned by root with permissions `-r--r--r--` and timestamp
`Jan 1 1970`, they live in the **Nix store** and cannot be edited in place.

In this case:

1. Confirm the store path:
   `readlink -f $XDG_CONFIG_HOME/tmux-a2a-postman/nodes/<node>.toml`
2. Find the editable source in dotfiles (typically
   `~/ghq/<user>/dotfiles/config/tmux-a2a-postman/nodes/<node>.toml`)
3. Apply all patches to the dotfiles source, not the deployed path
4. After patching, rebuild: `home-manager switch` (or equivalent) to redeploy

Report this as a constraint in findings, not as a skill failure.

## 6. Baseline Examples

The following examples illustrate the finding format.

### 6.1. Example 1 — Routing clarity (IMPORTANT)

```text
[IMPORTANT] Node: sender
Field: nodes/sender.toml:[sender].template
Check: Routing clarity
Result: FAIL
Issue: Template says "send a message via postman" without specifying a recipient.
Fix:
  "Send findings to coordinator. If coordinator is absent from
  talks_to_line, send to relay-node who will forward."
```

### 6.2. Example 2 — Completion protocol (IMPORTANT)

```text
[IMPORTANT] Node: approver
Field: nodes/approver.toml:[approver].template
Check: Completion protocol
Result: FAIL
Issue: No machine-readable approval signal defined. Recipients cannot parse
  the response programmatically.
Fix:
  "Reply with 'APPROVED: <summary>' when approving,
  or 'REJECTED: <reason>' when rejecting."
```

### 6.3. Example 3 — Cross-edge consistency (IMPORTANT, JUDGMENT-BASED)

```text
[IMPORTANT] Node: relay-node
Field: nodes/relay-node.toml:[relay-node].template
Check: Cross-edge consistency
Type: JUDGMENT-BASED
Result: FAIL
Issue: relay-node acts as a routing relay between reviewer and coordinator,
  but this is not documented. Forwarding-to-approver path is also absent.
Fix:
  "After receiving reviewer findings: if approved, forward to approver.
  If rejected, return to reviewer with specific revision request."
```

### 6.4. Example 4 — on_join completeness (MINOR)

```text
[MINOR] Node: worker
Field: nodes/worker.toml:[worker].on_join
Check: on_join completeness
Result: FAIL
Issue: on_join is empty; node receives no startup context.
Fix:
  on_join = "You are worker. Run 'tmux-a2a-postman -- help' on startup,
  then await task assignment."
```

## 7. Constraints

- Propose patches only; do NOT auto-apply
- When an issue is daemon-level (wrong edges, missing file, disabled session),
  note it as a config finding — template patches cannot fix it
- Manual integration test for `talks_to_line` visibility: if a node has not yet
  been discovered by PING in the current session, mark the result `INCONCLUSIVE`
  (environment), not a skill failure
