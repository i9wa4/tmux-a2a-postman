# Command Approvals

`execute-bash` is a wrapper for lightweight command approval choreography. It
records command metadata, asks for approval through a durable approval thread,
and applies the configured mode before running `bash -lc`.

## 1. Policy

Policies live in `postman.toml` under `[[postman.command_approval]]`.

```toml
[[postman.command_approval]]
requester = "worker"
label = "nix-build"
category = "verification"
reviewer = "orchestrator"
mode = "blocking"
approval_ttl_seconds = 900
```

`requester`, `label`, and `category` are match keys. Empty values and `*`
match any value for a `blocking` policy entry, but an `advisory`/`warn-only`
entry must pin all three to specific, non-wildcard values — a config with a
wildcard/empty `requester`, `label`, or `category` on a weak-mode entry fails
to load entirely (#831 D5/D7). `requester` is never a CLI flag: the effective
requester is always the calling pane's tmux title (#831 D4), the same trust
model `--record-decision`'s reviewer binding already uses, not a caller-
supplied value. CLI flags may override `reviewer` for a single command, may
only NARROW `mode` (`blocking` > `warn-only` > `advisory`) once an approver is
configured (#831 D1), and may only TIGHTEN (shorten), never lengthen, the
effective approval TTL (#831 D6).

`reviewer` is a plain audit label with no topology meaning.
`command_approver_node` is different: it names one real, configured node (same
family as `ui_node`) by marking that node in the `postman.md` Mermaid graph:

````markdown
## `edges`

```mermaid
graph LR
    worker --- orchestrator
    class orchestrator command_approver_node
```
````

It is global for the whole configuration. An absent approver leaves every mode
fail-open; a configured-but-unresolvable approver makes `blocking` fail closed.
See the rule below. Per-policy approver routing is not supported; every
execute-bash policy shares the single class-designated approver.

Migration note: legacy `[postman] command_approver_node` and
`[[postman.command_approval]] command_approver_node` keys in `postman.toml` are
ignored with a deprecation warning. Move the approver marker to `postman.md`
before relying on `blocking`; without a Mermaid approver, command approval
intentionally fails open and records `auto_approved_no_reviewer`. `get-status`
also reports ignored legacy TOML approver keys under
`command_approval.deprecated_command_approvers`; a migration is complete only
when both `command_approval.unresolved_command_approvers` and
`command_approval.deprecated_command_approvers` are absent.

### 1.1. Fail-open rule (#626)

An absent `command_approver_node` makes every command treated as approved, in
every mode, including `blocking`. Such commands are recorded with the decision
`auto_approved_no_reviewer`, distinct from a real recorded approval, so the
audit trail never confuses the two.

A configured-but-unresolvable `command_approver_node` is different. In
`blocking` mode the wrapper fails closed and does not run the command. In
`advisory` and `warn-only` modes, the existing nonblocking semantics remain:
`advisory` continues after recording and `warn-only` still requires an explicit
override. The unresolved name also produces a load-time warning and a visible
`command_approval.unresolved_command_approvers` marker in `get-status`.

Ignored legacy TOML `command_approver_node` values produce
`command_approval.deprecated_command_approvers` markers in `get-status`. Treat
that status field as a migration failure: the TOML key is ignored, so approval
remains fail-open unless a Mermaid `command_approver_node` is configured.

## 2. Running Commands

```sh
tmux-a2a-postman execute-bash \
  --label nix-build \
  --category verification \
  --reason "verify release build" \
  --command "nix build"
```

The wrapper stores requester, reviewer, label, category, mode, command digest,
reason, expiry, approval thread id, decision, and exit status. Full command
text is omitted by default. Add `--store-command-text` only when the command
body is safe to keep in the local audit log.

### 2.1. Positional argv fallback (#838)

`--command <bash>` is the documented interface: its value is trimmed of
leading/trailing whitespace and passed to `bash -lc`, with no further
reconstruction.

When `--command` is omitted, trailing positional arguments after `--`
become the command, with a rule based on how many there are:

- **Exactly one** positional argument is legacy, unchanged verbatim shell
  source: the same text and the same command digest as before #838 (joining
  a single element with any separator is a no-op).
- **Two or more** positional arguments are each individually POSIX-single-
  quoted and space-joined, reconstructing their original argv boundaries.

Before this fix, positional arguments of any count were joined with an
unquoted space (`strings.Join(fs.Args(), " ")`). For two or more arguments,
this lost their original boundaries: an invocation such as
`execute-bash ... -- bash -c "<script>"` collapsed to the flat string
`bash -c <script>`. When `bash -lc` re-parsed that string, its leading `bash
-c` consumed only the single next word of `<script>` as the nested shell's
own script text (bash -c's script argument is always just one word; every
later word becomes a positional parameter `$0`, `$1`, ... of that inner
invocation, not more of the script) — the inner shell's stdout is still
inherited by the caller, so nothing is silently discarded, but its script
was the wrong, truncated one. The outer shell then resumed at the next
operator or statement in the flattened text, so the visible effect varied
with the content: a dropped leading word, a misinterpreted `cd`, a
multi-word flag value re-split into extra positional arguments for whatever
program followed, and so on — not one single uniform failure mode.

**Upgrade note:** the two-or-more-argument case changes the reconstructed
command text, so its digest and default approval thread id also change
relative to pre-#838 behavior. A pending approval minted before upgrading
will not match the new digest; reusing its old `--thread-id` yields
`digest_mismatch`, and a fresh request is required. The one-argument case is
unaffected. Prefer `--command <bash>` for any script with more than one
shell operation; it needs no argv reconstruction at all.

## 3. Modes

`advisory` records the request and audit metadata, warns when approval is not
present, and continues.

`warn-only` records the request and refuses execution unless
`--override-approval` is supplied. The override is recorded in the audit event.

`blocking` refuses wrapper-mediated execution unless the matching approval
thread has a non-expired approved decision from the configured reviewer for the
exact command digest. Missing, stale, rejected, expired, wrong-reviewer, and
changed-digest approvals do not run. Missing, stale, rejected, expired, and
wrong-reviewer threads are retryable (#823 F-012): repeating the identical
command mints a fresh request instead of resurfacing the old terminal
diagnosis forever — see
[3.1](#31-waiting-for-the-decision-synchronously-823) below. A changed-digest
mismatch is a refusal, not a retryable state: it never mints a new request.

`blocking` is the default mode (#753) when neither a matching policy nor
`--mode` sets one explicitly. A configured `command_approver_node` that never
answers now halts the calling agent's work by default, where it previously
only produced an `advisory` warning; see
[3.3. Recovering when a blocking approval never lands](#33-recovering-when-a-blocking-approval-never-lands-753)
for how to get unstuck.

### 3.1. Waiting for the decision synchronously (#823)

With a trusted, resolvable `command_approver_node`, `blocking` mode keeps the
`execute-bash` call open by default and polls the local approval projection
(every 500ms) until the matching thread reaches a decision, a bounded
deadline passes, or the process is interrupted. On approval it runs the
command in the *same invocation* — the requester never has to inspect a
separate decision message and reconstruct or resubmit the original command.

```sh
tmux-a2a-postman execute-bash \
  --label nix-build \
  --category verification \
  --reason "verify release build" \
  --command "nix build"
# blocks here, polling, until the reviewer decides (or the wait times out)
```

- `--wait-timeout-seconds <n>` bounds how long the wait can run; the default
  is 300 (5 minutes). Waiting is never indefinite. There is no immediate-
  return escape hatch: `--no-wait` was removed (#831 D2), reversing #823's
  own intentionally-shipped opt-out — blocking mode always waits.
- The request's own expiry (`--approval-ttl-seconds`, default 900) and the
  wait timeout are two independent absolute deadlines; `expired` (exit 11)
  wins whenever the request's own expiry has passed, checked before
  `wait_timeout` (exit 12), including at exact equality (#831 D6). A caller
  should retry the identical call rather than lower
  `--wait-timeout-seconds`; see
  [Harness/caller timeout guidance](#32-harnesscaller-timeout-guidance-831)
  below.
- Fail-open (#626, no `command_approver_node` configured) and fail-closed (a
  configured but unresolvable `command_approver_node`) are unaffected by
  waiting — those decisions are made before the wait loop is ever
  considered, so an absent or unresolvable approver still behaves exactly as
  in [1.1. Fail-open rule](#11-fail-open-rule-626).
- Rejection, expiry, a wait timeout, cancellation (SIGINT/SIGTERM), a
  requester mismatch on a reused `--thread-id`, a failed or lost delivery to
  the approver, a context/session-generation change mid-wait, a repeated
  claim on an already-executed approval, a changed command digest, no live
  current session writer, and a few other terminal outcomes each surface a
  distinct `--json` `status` field and a distinct process exit code (10-23:
  rejected=10, expired=11, wait_timeout=12, cancelled=13, digest_mismatch=14,
  wrong_reviewer=15, stale=16, historical_only=17, requester_mismatch=18,
  delivery_failed=19, approver_lost=20, session_changed=21,
  already_executed=22, session_unavailable=23), so a caller scripting
  against `execute-bash` can distinguish "the approver said no" from
  "nobody answered in time" from "I was interrupted" without parsing the
  reason string.
- **#823 F-006 — read this before scripting against exit codes.** These
  exit codes are NOT guaranteed distinct from an executed command's own
  exit status: a command that itself exits 10 is numerically
  indistinguishable, on the bare exit code alone, from a rejected approval
  (also exit 10). The wrapper does not remap or reserve a code range the
  target command can never produce. The authoritative way to tell the two
  apart is the `--json` wrapper metadata's `status` field — one of the
  decision names above (the command never ran) versus `"exited"` with
  `exit_status` set (the command ran and exited with that status). Do not
  rely on the bare numeric exit code alone when that distinction matters.
- One approval is single-use (#823 F-001): once a real, thread-backed
  blocking-mode approval executes the command, an atomic claim event
  (`command_execution_claimed`) is journaled for that exact
  thread/input-request/command-digest. Every other invocation racing for
  the same approval — concurrent waiters, or a repeated call while the
  approval is still within its TTL — observes the existing claim and
  refuses with `already_executed` instead of running the command again.
  This changes the pre-#823 semantics, where a still-valid approved thread
  could authorize the command any number of times within its TTL. Fail-open
  (`auto_approved_no_reviewer`) and non-blocking-mode executions are not
  claimed — there is no scarce human decision to consume there.
- The wait binds itself to its starting correlation and re-checks it on
  every poll, and again immediately before accepting an approval or
  claiming it (#823 F-002/F-004/F-005): a retry against a still-pending
  thread reuses the thread's existing request instead of overwriting its
  correlation (which used to be able to strand the first waiter's approval
  reply), a decision on a thread belonging to a different requester is
  never accepted even when `--thread-id` and the command digest both
  match, and a context or session-generation change — even one landing in
  the narrow window right before the command is claimed and run — ends the
  wait with a distinct `session_changed` outcome instead of silently
  polling toward a generic timeout or claiming anyway.
- Retrying the identical command is safe and converges instead of
  permanently sticking on an old outcome (#823 rework-2, F-012/F-013). One
  unified rule governs every call: no request yet → a fresh one is created
  (two truly concurrent first callers still converge on exactly one
  request); a still-pending request → the exact same request is reused and
  re-delivered (this also recovers a request whose earlier delivery
  attempt failed, #823 F-013, without minting a duplicate); a request that
  ended rejected, expired, stale, historical-only, or wrong-reviewer → the
  next call atomically mints a fresh request superseding it, so none of
  those outcomes can block a future retry forever; a requester mismatch,
  digest mismatch, or unresolvable approver is a refusal and never mints
  anything; and an approved-and-already-executed thread stays
  `already_executed` until its own TTL expires, only then becoming
  retryable like any other expired thread. See
  [3.3](#33-recovering-when-a-blocking-approval-never-lands-753) for how to
  recover `already_executed` sooner than the TTL, if needed.
- Cancellation and the deadline always win over a late approval (#823
  F-005): both are checked before the wait loop accepts any decision on
  each iteration, again immediately before claiming, and again immediately
  after the claim succeeds and before the command actually runs — so a
  SIGINT/SIGTERM caught at any of these points, including precisely during
  the claim itself, still stops the command; the approval stays consumed
  (single-use either way) but never executes.
- A concurrent race for the same thread never lets the loser deliver a
  misleading prompt (#823 rework-3 F-015): when two callers race to mint or
  replace a request for the same thread id (for example two concurrent
  explicit `--thread-id` calls with different `--label`/`--category`), the
  loser validates its own requester, trusted approver, command digest, and
  policy against the request that actually won the race. On a mismatch it
  refuses with `requester_mismatch` and sends no prompt at all, rather than
  delivering a prompt built from its own policy under the winner's
  thread/input-request correlation.
- Blocking mode never falls back to a shadow-bootstrapped session for
  minting a request or claiming an approval (#823 rework-3 F-002): if there
  is no live current session writer yet (a genuine cold start, before this
  session has any daemon-owned or previously-bootstrapped state), the call
  refuses immediately with `session_unavailable` instead of racing a
  session bootstrap that could otherwise land two first-ever calls in two
  different generations. Advisory and warn-only modes are unaffected and
  keep their prior behavior. A later call in the same, now-bootstrapped
  session proceeds normally.
- Delivery failure and mid-wait approver loss are distinct, fast outcomes
  (#823 F-003): a request that fails to deliver to the approver returns
  `delivery_failed` immediately rather than waiting out the full timeout on
  a request the approver never saw, and the wait periodically re-verifies
  the approver is still discoverable, returning `approver_lost` if it
  disappears mid-wait.

### 3.2. Harness/caller timeout guidance (#831)

Since `--no-wait` is removed, a caller (a postman worker/agent invoking
`execute-bash`) whose command is not yet decided blocks for up to
`--wait-timeout-seconds` (default 300s) before receiving `wait_timeout`
(exit 12), rather than an immediate `blocked`/`pending` result.

- On `wait_timeout`, the underlying request is very likely still valid: a
  `wait_timeout` result can only occur when the wait deadline passed while the
  request's own expiry had not (see
  [3.1](#31-waiting-for-the-decision-synchronously-823)'s two-deadline model
  above; an already-expired request returns `expired` directly instead).
  Retrying the IDENTICAL `execute-bash` call (same
  `--label`/`--category`/`--command`, letting the deterministic thread id reuse
  the still-pending request) is correct, not wasteful.
- Do not busy-loop retrying with a very low `--wait-timeout-seconds` — that
  reintroduces the exact "poll externally" pattern #823 eliminated. Prefer
  the default (or a deliberately longer) timeout and let one call absorb the
  wait.
- Any external harness that invokes `execute-bash` as a subprocess and
  enforces its own kill/timeout on that subprocess must set that external
  timeout to EXCEED whatever `--wait-timeout-seconds` the invocation itself
  uses.
- A `cancelled` outcome applies only to a handled SIGINT/SIGTERM arriving
  while `execute-bash` is actively waiting. A SIGKILL is not handled and
  cannot be — the process is simply gone, with no `cancelled`/`wait_timeout`/
  `expired` outcome ever written.
- A caller killed after `execute-bash` has already claimed the single-use
  approval, but before the command actually started running, must make a
  fresh `execute-bash` request (a new approval thread); the consumed
  approval cannot be reused (`already_executed`, exit 22, is the likely
  outcome of a naive retry). A caller killed during the command's own
  execution should inspect for real-world side effects before retrying —
  the command runs via plain process execution with no wiring to the
  wrapper's own cancellation, so a kill mid-execution can leave partial
  effects or an orphaned child process.

### 3.3. Recovering when a blocking approval never lands (#753)

A `blocking` command that has requested approval but received no decision
by the time `--wait-timeout-seconds` elapses leaves a `pending` thread and
refuses to run. To recover:

1. Inspect the pending thread without re-running the command:

   ```sh
   tmux-a2a-postman inspect-command-approvals
   ```

   This shows the thread id, requester, reviewer, label, category, digest,
   reason, expiry, and current status for every outstanding request.
2. If the configured `command_approver_node` is reachable, have it record a
   decision through the normal path — either by replying `APPROVED: <reason>`
   or `NOT APPROVED: <reason>` to the delivered approval request (see
   [4.1](#41-delivery-to-a-valid-command_approver_node-626)), or by running
   `--record-decision` directly from that node's own pane:

   ```sh
   tmux-a2a-postman execute-bash \
     --thread-id command-approval-... \
     --record-decision approved \
     --reason "digest reviewed"
   ```

3. There is no `--mode` downgrade escape once a `command_approver_node` is
   configured (#831 D1): `--mode advisory` or `--mode warn-only` is refused
   as a policy-floor downgrade even when that configured approver is
   currently unresolvable — the exemption applies only to the genuinely
   ABSENT case ([1.1. Fail-open rule](#11-fail-open-rule-626), no approver
   configured at all). If the approver is unavailable and the command
   genuinely cannot wait, either have an operator reconfigure/remove the
   `command_approver_node` (accepting the broader fail-open consequences
   documented in 1.1), or use the direct-shell boundary below.
4. As a last resort, running the command directly in the shell (bypassing
   `execute-bash` entirely) is the explicit, documented escape boundary
   described in [7. Boundary](#7-boundary): the wrapper coordinates review, it
   is not a sandbox, so nothing prevents this — but it also means no approval
   thread, decision, or audit record is created for that run.

If instead a call reports `already_executed` (#823 F-001) for a command that
genuinely needs to run again, the approval that already ran it is single-use
and cannot be reused. Either wait for that approval's own TTL to expire —
the next call then mints a brand-new request automatically (#823 F-012) — or
pass an explicit `--thread-id` naming a fresh, not-yet-used thread id to
start an independent approval cycle right away.

## 4. Decisions

When `execute-bash` requests approval, it prints the approval thread id in the
wrapper metadata. The configured `command_approver_node`, running
`--record-decision` from its own pane, can decide the thread explicitly:

```sh
tmux-a2a-postman execute-bash \
  --thread-id command-approval-... \
  --record-decision approved \
  --reason "digest reviewed"
```

The decision's reviewer identity always comes from the calling process's own
tmux pane title, never from a flag; a `--reviewer` flag passed here has no
effect on the outcome. A caller whose pane identity is not the thread's
`command_approver_node` is refused with an error and no decision is recorded
(#626 B1-residual).

Use `--record-decision rejected` to reject a pending command.

### 4.1. Delivery to a valid command_approver_node (#626)

When a valid `command_approver_node` is configured and a command needs approval,
`execute-bash` also delivers a reply-required postman message to that node
directly, carrying the command hash, label, mode, and the approval thread id
— so the reviewer does not have to poll `inspect-command-approvals`. To
record a decision by replying instead of running `--record-decision`
directly, start the reply body with `APPROVED: <reason>` or
`NOT APPROVED: <reason>` and keep all three generated correlation fields in
the reply's own frontmatter: `thread_id`, the exact
`fills_input_request_id`, and the exact `command_hash`; the daemon records the
decision automatically on delivery. A
reply on a command approval thread whose body does not start with one of
those two prefixes is logged as a warning and not recorded as a decision at
all — use `--record-decision` directly if you need to attach a reply body
that doesn't fit that convention. Delivery itself is best-effort — a
delivery failure (for example, the command_approver_node is not currently
discoverable) is logged but never blocks or duplicates the already-journaled
approval request.

Only a reply whose sender exactly matches the request's stored
session-qualified `command_approver_node` address is ever honored; a reply from
anyone else leaves the request pending and has no effect on the command,
regardless of what the policy's `reviewer` audit label says (#626 B1) — the
`reviewer` label itself is a plain, requester-influenceable string and has no
bearing on this check.

The same authenticated-caller requirement applies to `--record-decision` (#626
B1-residual): the decision's reviewer identity is always the calling process's
own tmux pane title, never the `--reviewer` flag. A caller whose pane identity
is not the thread's `command_approver_node` is refused outright, with no
decision recorded — passing `--reviewer <command_approver_node_name>` on the
command line has no effect on this check, since that name is a plain, readable
config value, not proof of who is actually calling.

## 5. Inspection

Command approval state is inspectable without re-running the command:

```sh
tmux-a2a-postman inspect-command-approvals
```

The output shows each approval thread with requester, reviewer, label,
category, digest, reason, expiry, timestamps, and status.

## 6. Decision History

Approver approval and rejection decisions are also accumulated as individual
JSON files under:

```text
<session-dir>/command-approval-decisions/
```

Each file is named from the underlying journal event sequence and id. The
layout is intentionally file-oriented instead of JSONL so maintainers can
inspect, copy, remove, or archive one decision record at a time with ordinary
filesystem tools.

The schema includes:

- `thread_id`, `decision`, and `effective_status`
- requester, reviewer audit label, and trusted `command_approver_node`
- label, category, mode, command digest, and request/decision reasons
- requested, expiry, and decided timestamps
- `decision_message_id` when the decision came from a reply

Raw command text is omitted by default. It appears only when the original
request opted into existing `--store-command-text` audit behavior, so the
decision history keeps the same privacy boundary as the durable journal.

Allowlist maintainers can search the accumulated history directly, for example:

```sh
find "$SESSION_DIR/command-approval-decisions" -name '*.json' -print
rg '"decision":"approved"|"decision":"rejected"' "$SESSION_DIR/command-approval-decisions"
```

No automatic retention policy deletes these records. Treat them as local,
owner-only audit data and rotate or archive the directory according to the
operator's local retention policy.

## 7. Boundary

This is coordination, not enforcement. `execute-bash` does not sandbox bash,
prevent direct shell execution, prevent another process from running the same
command, or enforce OS-level policy. Use it to make agent command review
explicit and auditable inside a Postman session.

## 8. Upgrade Notes (#831)

For operators upgrading an existing deployment past #831's implementation:

- Any single bad `command_approval` policy entry now fails the WHOLE config
  load (D7 Part B) -- not just daemon startup: `start`, `stop`,
  `execute-bash`, `send-heredoc`, `pop`, `capture-profile`,
  `get-status`/`get-status-oneline`, and `inspect-message` all load the same
  config independently, so a noncompliant config breaks all of these
  commands, not only the daemon process.
- PREFLIGHT BEFORE UPGRADING: run `tmux-a2a-postman get-status` (or
  `get-status-oneline`) against your current config using the NEW/candidate
  binary before deploying this change. `get-status` already loads config as
  part of its normal operation, is read-only and safe to run repeatedly.
  IMPORTANT: this only works with the new binary -- your CURRENTLY-RUNNING
  (old) binary has no knowledge of these new rules and will report an
  existing config as fine even when it would fail to load under them.
- BEFORE upgrading, pin `Requester`+`Label`+`Category` together (non-
  wildcard, specific values) on every `advisory`/`warn-only` policy entry,
  and fix any `Mode` string typos in your config. A config that currently
  relies on a fail-open, wildcard-permissive weak-mode entry (e.g.
  `Label: "*"` or no `Requester` pinned) will REFUSE TO LOAD after this
  change -- this is the intentional point of D5/D7, but it should be
  discovered during preflight, not as a surprise command failure after
  upgrading.
- `--no-wait` and `--requester` are removed entirely. A caller still passing
  either gets a clear migration-guidance error (not Go's generic "flag
  provided but not defined"). Callers relying on `--no-wait`'s old
  immediate-return behavior must adjust to `execute-bash` always waiting for
  a blocking-mode decision, bounded by `--wait-timeout-seconds`; callers
  relying on `--requester` must rely on the calling pane's own tmux title
  instead (D4).
