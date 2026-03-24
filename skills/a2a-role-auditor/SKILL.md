---
name: a2a-role-auditor
description: |
  Role template auditor for tmux-a2a-postman multi-agent systems.
  Audits node role definitions (postman.toml / postman.md / nodes/*) to fix node-to-node interaction breakdowns.
  Use when:
  - A node behaves unexpectedly (routes wrongly, ignores messages, approves nothing)
  - Nodes can't see each other in talks_to_line (after confirming session/PING issue is ruled out)
  - Adding a new node and need to verify its template is complete and consistent
  - User wants to review or improve role definitions for any node
  Do NOT use for daemon-level failures (dead-letter from routing/edge misconfiguration);
  run triage first to determine if the issue is template-level.
---

# a2a-role-auditor

Audits node role templates (`postman.toml`, `postman.md`, and/or `nodes/*`) in a
tmux-a2a-postman project to fix node-to-node interaction breakdowns.

## 1. Mandatory Triage Gate

Before running any template audit, determine whether the issue is daemon-level
or template-level.

**Daemon-level indicators** (stop here — report as config issue, do NOT produce
patches):

- Wrong or missing edges in `postman.toml` or `postman.md`
- Node not defined in `postman.md`, `nodes/{node}.toml`, or `nodes/{node}.md`
- Session disabled in postman TUI

**Template-level confirmed** (node exists, edges correct, but behavior is
wrong):

- Proceed to the 10-check audit below.

## 2. 10-Check Audit

### 2.1. Pre-check: File Existence (binary)

For every node referenced in edges, verify the node is defined in one of:
`postman.md`, `nodes/{node}.toml`, or `nodes/{node}.md`.

- PASS: node definition found
- FAIL: node not defined → emit BLOCKING finding; abort all further checks for
  that node

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

### 2.6. Check 5 — Messaging Protocol Instructions

- PASS: template contains instruction to use `send-message` as the primary
  messaging command (e.g., "tmux-a2a-postman send-message --to <node> --body")
- PASS (also acceptable): template mentions `create-draft` as an advanced
  alternative for long messages
- PASS (also verify): template mentions `next` for reading messages
  (read + archive in one step) and/or `count` for inbox status
- PASS (also verify): template does NOT instruct the agent to use
  `mv draft/ post/`; agents must use `tmux-a2a-postman send <filename>` to
  submit drafts
- PASS (also verify): template does NOT instruct the agent to use `mv` to move
  files to `read/`; agents must use `tmux-a2a-postman archive <filename>` to
  mark messages as read
- PASS (also verify): template does NOT reference raw filesystem paths
  (e.g., `~/.local/state/tmux-a2a-postman/...`); use CLI commands like
  `get-session-health` instead (#287: filesystem internals hidden from agents)
- FAIL: template lacks `send-message` instruction — agents use the verbose
  3-step create-draft workflow instead of the atomic one-step command
- FAIL: template lacks `next` or `count` — agents use the old `read` + manual
  cat + `archive` workflow instead of the streamlined commands
- FAIL: template instructs `mv draft/ post/` — deprecated; use
  `tmux-a2a-postman send <filename>`
- FAIL: template instructs `mv inbox/... read/` or equivalent — deprecated; use
  `tmux-a2a-postman archive <filename>`
- FAIL: template references raw filesystem paths for monitoring (e.g.,
  `ls ~/.local/state/.../waiting/`) — use `get-session-health` instead

### 2.7. Check 6 — Pre-Approval Verification

Applies only to nodes whose template contains APPROVED or REJECTED signal words
(typically reviewer or approver nodes).

- PASS: template contains an explicit verification step before issuing verdict
  (e.g., "verify artifact exists with git status")
- FAIL: template issues APPROVED/REJECTED without requiring artifact
  verification — approvals based on plan text alone are unreliable

### 2.8. Check 7 — draft_template Disclaimer

Applies only to nodes that define a `draft_template` field.

- PASS: `draft_template` includes a disclaimer such as "(for context only — only
  nodes in 'You can only talk to:' are reachable)"
- FAIL: `draft_template` is present but lacks a reachability disclaimer — agents
  may assume all nodes listed in the template are contactable, leading to
  dead-lettered messages

### 2.9. Check 8 — Dropped Ball Timeout Configured

Applies to all non-observer nodes (nodes whose role does NOT contain
"observer").

- PASS: `dropped_ball_timeout_seconds` is greater than 0
- FAIL: `dropped_ball_timeout_seconds` is 0 or absent — the node can hold the
  ball indefinitely without triggering a dropped-ball alert, causing silent
  stalls

### 2.10. Check B-I8 — Protocol Reminder Presence

- PASS: template references the postman protocol (e.g., contains
  "tmux-a2a-postman --help", "protocol", "tmux-a2a-postman", "send-message",
  or "create-draft")
- FAIL: template lacks any protocol reminder — agents may ignore messaging
  conventions, leading to malformed messages or manual file creation

## 3. Findings Format

Every finding MUST use this exact schema:

```text
[SEVERITY] Node: {node}
Field: {source-file}:{field}
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

| File                             | Path                                                                          |
| -------------------------------- | ----------------------------------------------------------------------------- |
| Structural config (TOML)         | `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml`                             |
| Templates config (Markdown)      | `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md`                               |
| Node template files (TOML)       | `$XDG_CONFIG_HOME/tmux-a2a-postman/nodes/{node}.toml`                        |
| Node template files (Markdown)   | `$XDG_CONFIG_HOME/tmux-a2a-postman/nodes/{node}.md`                          |
| Default config reference         | `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.default.toml`                     |
| Project-local structural config  | `.tmux-a2a-postman/postman.toml` (project root, walked up from CWD)          |
| Project-local templates          | `.tmux-a2a-postman/postman.md` (highest priority for templates)              |

- `$XDG_CONFIG_HOME` defaults to `~/.config` if unset.
- `postman.toml` defines `[postman]` section (edges, timing) and `[node-name]`
  sections (structural per-node config like `dropped_ball_timeout_seconds`).
- `postman.md` defines templates, edges (Mermaid), common_template, and
  per-node role/template. Node sections use h2 backtick headings;
  role uses an h3 backtick heading within each node.
- Load order: `postman.toml` -> `nodes/*.toml` -> `nodes/*.md` -> `postman.md`
  (last wins for overlapping fields).
- `postman.default.toml` is a canonical reference listing all configurable
  values with their defaults and comments. Consult it when auditing
  `dropped_ball_timeout_seconds` defaults and other per-node configurable fields
  (#249 policy: all values must appear explicitly here).

## 5. System-Provided Context (Do Not Duplicate in Role Templates)

Before auditing role templates, read `postman.default.toml` to understand what
information the system already provides to agents at message delivery time.
Role templates should NOT repeat information that the system injects
automatically — doing so creates maintenance burden and drift risk.

**How to check**: read the embedded default config at
`internal/config/postman.default.toml` in the tmux-a2a-postman repository.
The Template Variables Reference comment block at the top of the file lists
all available variables per template type.

Key templates that inject context automatically:

| Template            | Injected at                  | Key variables provided                        |
| ------------------- | ---------------------------- | --------------------------------------------- |
| `draft_template`    | draft/send-message creation  | `{template}` (recipient role), frontmatter    |
| `message_footer`    | appended to delivered message | `{can_talk_to}`, `{sender}`, `{reply_command}`|
| `notification_template` | sendkeys to pane on arrival | `{node}`, `{from_node}`                      |
| `message_template`  | daemon ping delivery         | `{role_content}`, `{talks_to_line}`           |

**Audit implication**: if a role template repeats any of the following, flag it
as MINOR (unnecessary duplication, not a bug):

- Reply command instructions (provided by `message_footer`)
- "You can talk to" lists (provided by `message_footer` `{can_talk_to}`)
- Recipient role content (provided by `draft_template` `{template}`)
- Edge violation warnings (handled by daemon `edge_violation_warning_template`)

## 6. Workflow

1. Read config — `postman.toml` for structural config, `postman.md` for
   templates. Extract edges and build adjacency map.
2. Read node definitions from `postman.md` (h2 backtick sections)
   and/or `nodes/*.toml` / `nodes/*.md`. Project-local overrides XDG.
3. For each node: run Pre-check, then Checks 1–8 and B-I8 in order
4. Produce findings report sorted by severity
5. Propose concrete patch text for every finding
6. Present to user for feedback; iterate until approved

NOTE: Do NOT auto-apply patches. Propose only; the user applies manually or
delegates to a worker node.

### 6.1. Nix Store Warning

Before attempting to patch any config file, check if the deployed path is
read-only:

```sh
ls -la $XDG_CONFIG_HOME/tmux-a2a-postman/
```

If files are owned by root with permissions `-r--r--r--` and timestamp
`Jan 1 1970`, they live in the **Nix store** and cannot be edited in place.

In this case:

1. Confirm the store path:
   `realpath $XDG_CONFIG_HOME/tmux-a2a-postman/postman.md`
2. Find the editable source in dotfiles (typically
   `~/ghq/<user>/dotfiles/config/tmux-a2a-postman/postman.md`)
3. Apply all patches to the dotfiles source, not the deployed path
4. After patching, rebuild: `home-manager switch` (or equivalent) to redeploy

#### Alternative (preferred): use project-local override

Instead of editing dotfiles, create a project-local config that overrides
the Nix-deployed version:

```sh
mkdir -p .tmux-a2a-postman
# Create postman.md with template overrides:
cp "$(realpath $XDG_CONFIG_HOME/tmux-a2a-postman/postman.md)" \
   .tmux-a2a-postman/postman.md
# Edit .tmux-a2a-postman/postman.md directly
```

This file is in your project repo, editable immediately, and versioned with git.
No `home-manager switch` required.

Report this as a constraint in findings, not as a skill failure.

## 7. Baseline Examples

The following examples illustrate the finding format.

### 7.1. Example 1 — Routing clarity (IMPORTANT)

```text
[IMPORTANT] Node: sender
Field: postman.md:## `sender` template
Check: Routing clarity
Result: FAIL
Issue: Template says "send a message via postman" without specifying a recipient.
Fix:
  "Send findings to coordinator. If coordinator is absent from
  talks_to_line, send to relay-node who will forward."
```

### 7.2. Example 2 — Completion protocol (IMPORTANT)

```text
[IMPORTANT] Node: approver
Field: postman.md:## `approver` template
Check: Completion protocol
Result: FAIL
Issue: No machine-readable approval signal defined. Recipients cannot parse
  the response programmatically.
Fix:
  "Reply with 'APPROVED: <summary>' when approving,
  or 'REJECTED: <reason>' when rejecting."
```

### 7.3. Example 3 — Cross-edge consistency (IMPORTANT, JUDGMENT-BASED)

```text
[IMPORTANT] Node: relay-node
Field: postman.md:## `relay-node` template
Check: Cross-edge consistency
Type: JUDGMENT-BASED
Result: FAIL
Issue: relay-node acts as a routing relay between reviewer and coordinator,
  but this is not documented. Forwarding-to-approver path is also absent.
Fix:
  "After receiving reviewer findings: if approved, forward to approver.
  If rejected, return to reviewer with specific revision request."
```

## 8. Constraints

- Propose patches only; do NOT auto-apply
- When an issue is daemon-level (wrong edges, missing file, disabled session),
  note it as a config finding — template patches cannot fix it
- Manual integration test for `talks_to_line` visibility: if a node has not yet
  been discovered by PING in the current session, mark the result `INCONCLUSIVE`
  (environment), not a skill failure
