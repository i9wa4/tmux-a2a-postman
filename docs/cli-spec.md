# CLI specification

This is the canonical input/output contract for the daemonless CLI agreed in
[issue #937](https://github.com/i9wa4/tmux-a2a-postman/issues/937).
This document describes the implementation in this change. Skills describe
workflows separately from this reference.

## 1. Commands and common rules

| Command             | Purpose                                                     |
| ------------------- | ----------------------------------------------------------- |
| `get-status`        | Read the current workspace's node summaries                 |
| `pop`               | Claim one own unread mail and return its final archive path |
| `send-heredoc`      | Send a request or reply to claimed message IDs              |
| `execute-heredoc`   | Review and execute source in the caller's shell environment |
| `approve-execution` | Send approval for one claimed execution request             |
| `decline-execution` | Send rejection for one claimed execution request            |
| `help [topic]`      | Explain commands without initializing workspace state       |
| `version`           | Print name, version and commit as JSON                      |

Operational commands accept `--config <absolute-postman.md>` and `--help`.
The default is `$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md`, or
`$HOME/.config/tmux-a2a-postman/postman.md` when XDG_CONFIG_HOME is unset.
The top-level `--config` selects the same configuration. Relative or duplicate
options, unexpected positional arguments and removed options are errors.
`--postman-md` is removed from the product CLI. The notification test driver's
similarly named fixture-copy option is a separate development interface.

The caller comes from verified native tmux identity. Mail remains bound to the
original HOME-relative workspace across `cd`; execution uses its explicit,
captured cwd. Runtime replacement preserves durable mail. Unknown ownership
never grants permission to mutate or execute. No daemon startup, registration,
model API or recurring POP loop is required.

Except for execution's child streams, outputs are JSON. Times are UTC RFC3339.
`as_of` is when mail facts were aggregated; it is not a capture timestamp.
Missing or unverifiable values are `null` with a useful reason, never invented
zero counts. A failed supplemental observation does not undo a committed send,
claim, decision or completed command.

## 2. Common node summary

Both a get-status row and operation `self` use these fields:

```json
{
  "node": "worker",
  "unread_count": 0,
  "inbound_count": 0,
  "outbound_count": 0,
  "draft_count": 0,
  "failed_count": 0,
  "last_pop_at": null,
  "last_capture_at": null,
  "last_screen_change_at": null
}
```

| Field                   | Meaning                                                           |
| ----------------------- | ----------------------------------------------------------------- |
| `unread_count`          | Message files in this node's inbox                                |
| `inbound_count`         | Received reply-required requests without an exact sent reply      |
| `outbound_count`        | Sent reply-required requests without an arriving exact reply      |
| `draft_count`           | Fully staged messages whose delivery/save is not complete         |
| `failed_count`          | Preserved messages with confirmed non-delivery evidence           |
| `last_pop_at`           | Latest successful claim receipt's time, including internal claims |
| `last_capture_at`       | Latest successfully saved observation of the current native pane  |
| `last_screen_change_at` | Time a snapshot observed a difference from its valid predecessor  |

An empty POP does not update last_pop_at. Copying files or changing mtimes does
not update it either. The first pane snapshot has no preceding difference;
changing pane/process incarnation resets that comparison. Screen changes do
not establish progress, notification acceptance, comprehension or task success.
State-machine labels such as working/idle are deliberately deferred.

Counts concern message files, not receipts or failure sidecars. POP alone never
closes a reply obligation. A matching reply closes its sender's outbound wait
as soon as it arrives, even before that reply is POP'd. A combined reply closes
each explicitly referenced request once and adds only one unread message.

## 3. get-status

```sh
tmux-a2a-postman get-status
```

There are no command-specific options or positional arguments. The normal result
has `workspace`, `as_of` and `nodes`, listing every configured local node. Only
a verified local interface caller additionally receives `parent` and `children`:
the directly related workspaces' interface nodes, not all their agents. A
configured unavailable relation is retained with null counts, its session
selector and a reason. A verified interface with no parent has `parent: null`;
one with no children has `children: []`. Ordinary callers omit both keys. No
sibling, grandchild or directory-ancestry inference is allowed.

This command reads saved files only. It does not initialize storage, claim mail,
recover transactions, send notifications, capture panes or delete observations.

## 4. pop

```sh
tmux-a2a-postman pop
```

No command-specific options or positional arguments are accepted. One eligible
own inbox message is exclusively moved to read and receives a durable receipt.
The existing selection order is preserved; random message IDs are not a FIFO
timestamp contract. Output fields are `workspace`, `as_of`, `success`,
`markdown_absolute_path` and common `self`.

A successful claim returns the absolute existing final Markdown path. Empty
POP succeeds with a null path and unchanged last_pop_at. Read the complete
Markdown through EOF: sender, identity and reply guidance come from that file,
not repeated message bodies/receipt paths in JSON. The notification's hinted ID
need not be the message actually selected; respond to the mail actually read.

On a claim error, success is false and reason explains it. A committed claim is
not resurrected if stdout, capture or supplemental state fails. Only the
approval wrapper has internal exact-response selection; that is not a public
POP argument.

## 5. send-heredoc

```sh
tmux-a2a-postman send-heredoc --to worker <<'POSTMAN_BODY'
Please check the change and reply with the result.
POSTMAN_BODY
```

`--to` is the only dedicated option. A configured local node or authorized
direct `session:interface-node` route creates a new reply-required request.
A claimed message ID replies to that message's exact sender and return route;
the CLI recovers correlation from its read archive and receipt. A reply does
not create another reply obligation. Mid-task questions are new node-addressed
requests and leave the original obligation open.

```sh
tmux-a2a-postman send-heredoc --to m-0123456789abcdef0123456789abcdef <<'POSTMAN_BODY'
The check is complete.
POSTMAN_BODY
```

Comma-separated claimed IDs create one combined reply, currently bounded to
16 IDs. All must belong to this caller and have one identical sender and
workspace/session return route. Validate every ID before delivery; invalid or
duplicate IDs do not cause a partial reply. Different recipients require
separate sends. Old stored mail's reply policy is never reinterpreted.

The body is literal non-terminal stdin from a quoted heredoc; bounds are
1–61440 input bytes, subject to the durable record's JSON-escaped size limit.
There is no body argv, --reply-to, --fills-input-request-id, --reply-required,
--no-reply or capture toggle. Generated guidance is versioned separately from
the exact user body. It shows the actual reply ID and says when no reply is due.

Normal JSON contains workspace/as_of/success/self. Durable delivery and sender
archival define mail success. A failed or ambiguous notification is additional
`notification` evidence and never undoes delivery. Operation failure includes
reason and a recovery message ID when available. Do not automatically send the
body again because capture/notification/output was unsuccessful.

Complete messages are staged in draft before publication. Confirmed undelivered
messages go to failed with their original ID, full body and a reason. An
uncertain outcome remains in draft/recovery records; recovery checks inbox/read
and sent copies and must never recreate an already claimed inbox delivery.

## 6. Notifications and snapshots

Send completes a bounded foreground notification attempt before returning. It
does not await POP, understanding, a reply or task completion. The target is a
verified recipient pane; ordinary shells and tmux copy/input modes are rejected.

Postman processes one target pane's input at a time, with an OS file lock shared
by all senders to that destination. It pastes one short hint, observes the full
hint at the cursor, allows a short settling period and sends one real C-m.
Observe after submission before admitting another hint. Lock waiting is bounded
to five seconds; input confirmation gets a fresh two seconds after acquisition,
with an eight-second total post-commit bound. Waiting does not repeatedly
refresh native bindings. No fixed Codex/Claude
prompt symbols or model APIs determine acceptance. Unknown results are retained
and do not cause blind re-paste or repeated Enter. A lock released by process
exit does not erase a pending/unknown input record. Human drafts and unrelated
writers remain outside this Postman-only serialization contract (#901).

Valid send and POP, including empty POP, trigger a bounded saved-text snapshot
of every eligible pane across the caller's verified session windows. Parent or
child sessions refresh their own snapshots; get-status does not refresh them.
Notification confirmation may additionally inspect only its authorized target
pane. Unconfigured panes keep native identity in observation storage, not fake
agent mailboxes. Linked windows, process changes and partial coverage are
explicit.

Snapshot timing and implementation are chosen by measured CLI latency and
coverage. Any asynchronous helper must be finite and detach inherited streams
and locks. Observation errors never change mail results or failed mail counts.
Saved snapshots and logs remain until the user explicitly deletes them. There
is no automatic expiry, pruning, rotation or OS cleanup task. Per-invocation
bounds limit one capture without deleting older history. The CLI maintains
bounded latest-observation indexes instead of rescanning all retained text.

## 7. execute-heredoc

```sh
tmux-a2a-postman execute-heredoc --reason 'Check the change for regressions' <<'POSTMAN_COMMAND'
go test ./...
POSTMAN_COMMAND
```

Source is literal quoted heredoc stdin; --command is removed. The source,
reason, resolved shell and caller cwd identify this invocation. After review,
a fresh interactive shell initializes using the caller's normal configuration
and applicable project environment. The target is the caller's execution
environment, not the approver's machine. Initialization errors do not silently
fall back to another shell/environment. Specific TTY/stream handling is covered
by the implementation and process tests, not a promise to clone an existing
shell's complete in-memory state.

No configured approver, or positively verified current absence, permits advisory
execution. Present approvers receive an ordinary durable request and the common
input notification. Unknown configuration/identity does not permit execution.
The CLI waits up to 300 seconds for its exact decision; the blocked caller AI
does not need to POP. Rejection, timeout, cancellation or approver loss never
converts a waiting call to advisory. No mailbox/notification lock spans waiting
or execution. The 300 seconds bound approval waiting, not approved runtime.

Stdout/stderr are streamed, not permanently logged by Postman. The final JSON is
a distinct last stderr line with common workspace/as_of/self, success, executed,
exit_code and approval facts. executed=false means confirmed not run;
executed=null means possibly started with unknown outcome. Preserve a known
command exit code; wrapper failures are nonzero and structurally
distinguishable. Approval reason and system failure reason are separate. Never
automatically repeat an interrupted possibly started execution. Audit is minimal
outcome and correlation evidence, not a reusable grant or full output-retention
platform.

## 8. approve-execution and decline-execution

```sh
tmux-a2a-postman approve-execution --to m-0123456789abcdef0123456789abcdef --reason 'The requested test is appropriate'
```

Alternatively:

```sh
tmux-a2a-postman decline-execution --to m-0123456789abcdef0123456789abcdef --reason 'The execution conditions need clarification'
```

The action is fixed by the command name. There are no decision strings, boolean
flags or implicit default decisions. Both ID and reason are explicit. The target
must be this approver's claimed execution request; the CLI obtains exact source
and correlation itself and creates the decision mail. It rejects invalid,
unclaimed, wrong-kind or duplicate/conflicting decisions before sending.

The approval request shows its exact ID and the two command examples. No JSON,
source, command hash or manual correlation is copied by the approver. These
commands send a decision and return normal mail delivery/self output; they do
not execute the source or grant permission for another invocation. A matching
active waiter consumes the decision directly, without another POP hint into
the requester's busy input editor. Late decisions cannot authorize a new run.

## 9. Verification

The root process tests exercise the actual public main and OS exit boundary.
Contract tests cover malformed/removed inputs, compact output, independent
combined replies, read-only status, claim concurrency and lost stdout,
unavailable interfaces, capture failure, exact approval decisions, cancellation,
reviewer loss, late decisions and independent repeated execution. Session tests
cover all-window native text, unconfigured panes, linked-window exclusion,
replacement, change clocks and retained history. Shell tests cover real Bash/Zsh
startup, aliases/functions, first-prompt hooks, approved direnv/project PATH and
streams.

`TestNotificationDrivenTerminalAgents` and `nix run .#notification-smoke` use
the same twenty-case deterministic fixture, including four senders submitting
eight simultaneous requests to one approver. The receiver alone runs POP, reads
the archive and replies. Additional cases preserve the production-shaped
numbered Markdown configuration. Optional real Codex TUI checks replace model
decisions with a local fixed-response server; production never uses that API.

Required integrated checks are `go test ./...`, `nix flake check`, `nix build`,
Go linting and skill validation. See the PR's validation results and CI for the
exact tested revision and platform evidence. Claude model behavior is not
asserted by terminal fixtures, and screen observations never prove
comprehension.

## 10. Complete interface and logging inventory

The consumers are agents invoking the installed CLI, people diagnosing a run,
and the isolated development fixtures. Historical entries below describe the
main interface before this change; removed syntax is intentionally rejected.

### 10.1. Inputs and entry points

| Surface                                                               | Decision and purpose                                               | Callers and rationale                                                                    |
| --------------------------------------------------------------------- | ------------------------------------------------------------------ | ---------------------------------------------------------------------------------------- |
| `get-status`                                                          | Keep, simplify to workspace nodes and authorized direct interfaces | Agents/operators need one saved-file view; no global workspace scan                      |
| `pop [id]`                                                            | Simplify to argument-free `pop`                                    | Receivers claim their actual next mail; notification IDs remain hints                    |
| `send-heredoc --to node`                                              | Keep; new requests require replies                                 | Senders need explicit configured routes and literal stdin                                |
| `--to session:interface`                                              | Keep direct parent/child routes                                    | Interface nodes need exact per-hop identity without foreign capture access               |
| `--reply-to`, `--fills-input-request-id`                              | Remove from public input; derive from `--to <POP'd-id>`            | Agents should not transcribe correlation metadata                                        |
| Comma-separated `--to` IDs                                            | Add bounded combined replies, maximum 16                           | One answer can resolve several claimed requests with one sender/route                    |
| `--reply-required`, `--no-reply`                                      | Remove; request/reply semantics determine policy                   | Avoid contradictory reply flags and endless reply obligations                            |
| `execute-bash --command`                                              | Replace with `execute-heredoc --reason` and stdin                  | Exact multiline source, cwd and initialized shell form one reviewable request            |
| `approve-execution`, `decline-execution`                              | Add fixed actions with `--to` and `--reason`                       | Approvers choose one action without JSON/code copies or boolean ambiguity                |
| Decision/helper stdin                                                 | Reject nonempty stdin                                              | Reasons and target are explicit flags; source is never copied by the approver            |
| `--config`                                                            | Keep one absolute override, before or after the command            | Operators/fixtures need explicit topology; defaults are cwd-independent                  |
| `--postman-md`                                                        | Remove product alias                                               | One spelling for one input; the smoke driver's fixture-copy flag is separate             |
| `--capture-related-panes`                                             | Remove; valid send/POP always trigger bounded session capture      | Visibility should not depend on each agent remembering a flag                            |
| `help [topic]`, `<command> --help`, top-level `--help`                | Keep without runtime state; reject extra help arguments            | Installed syntax belongs in help, workflows in skills                                    |
| `version`, top-level `--version`                                      | Keep compact name/version/commit JSON                              | Operators need the actual installed revision                                             |
| Bare executable                                                       | Keep usage display, no mutation                                    | No implicit startup operation                                                            |
| Duplicate/unknown flags and extra positionals                         | Reject before mail publication/execution                           | No last-value-wins decisions or hidden compatibility branches                            |
| HOME and XDG_CONFIG_HOME                                              | Keep absolute, canonical ownership/config sources                  | Agents/operators use normal XDG layout; relative values fail                             |
| XDG_STATE_HOME                                                        | Keep absolute private state selection                              | Tests and operators isolate mail; no new root flag                                       |
| TMUX, TMUX_PANE and tmux native evidence                              | Keep verified identity inputs                                      | Mutable names, cwd alone and PID liveness alone are insufficient                         |
| tmux default-shell, cwd and exported caller environment               | Keep explicit execution environment                                | Fresh shell configuration and first-prompt hooks run after authorization                 |
| Legacy TOML, POSTMAN_HOME, context/root/daemon overrides              | Remove/ignore as authority                                         | They must not gate or redirect current mail                                              |
| Daemon start/stop/RPC, observer, model adapters, grants/resume policy | Remove                                                             | No resident process, retry service or second state engine                                |
| Model-specific idle/compaction and unused capture-registration ledger | Remove dormant code                                                | Deferred state-machine work must not masquerade as current authority                     |
| `nix run .#notification-smoke` and Python smoke options               | Keep development-only                                              | Fixture/local-model/live modes, case/terminal selection, reports and config-copy testing |
| Nix update-toolchain, skill-check/publish and release helpers         | Keep explicit development/release tools                            | They do not add Postman operational commands; publishing remains user/release-owned      |

### 10.2. Outputs

Before, send returned `status`, a full metadata envelope and a separate notice;
POP returned metadata, archive and receipt paths; self mixed obligation arrays,
peer guidance, next-action and activity labels. These are replaced with the
common counts/timestamps documented above. The full immutable mail remains in
its archive. A notification never exposes the body.

| Output                                                              | Decision and reason                                                                                     |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `workspace`, `as_of`, operation `success`, common `self`            | Keep compact context and operation result on every mail outcome                                         |
| `nodes`, interface `parent`/`children`                              | Keep explicit scope; unavailable related interfaces remain unknown                                      |
| `markdown_absolute_path`                                            | Keep only POP's final archive path; empty is null                                                       |
| Metadata/body/receipt echo, old status labels, next-action/activity | Remove redundant or inferred fields; archives and file facts are authoritative                          |
| `notification`                                                      | Show only unavailable/unknown; never reverse delivery success                                           |
| `reason`, `recovery_message_id`                                     | Keep failure diagnosis and stable recovery identity without a resend instruction                        |
| Null counts/times                                                   | Keep unknown distinct from zero; diagnostics cannot change operation success                            |
| Child stdout/stderr and exit                                        | Keep distinct streams; only final execution JSON/progress uses stderr                                   |
| `executed`, `exit_code`, `approval`                                 | Separate run/no-run/uncertain outcome and reviewer's reason from system failure                         |
| Emoji error prefix and repeated command error text                  | Remove after structured errors; retain a single diagnostic for global/unknown commands or broken output |
| Help/error workflow dumps                                           | Simplify to syntax and relevant diagnosis; skills retain workflows                                      |

### 10.3. Every log and persisted record destination

| Destination                                                                    | Purpose, audience and decision                                                                                                          |
| ------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------- |
| stdout, operational CLI                                                        | One compact JSON result for agents; no banners, raw mail or debug prose                                                                 |
| stdout, execution child                                                        | Exact child stream for user pipelines; no Postman prose                                                                                 |
| stderr, execution                                                              | Exact child stream plus short waiting notice and final JSON line; add only a necessary line boundary                                    |
| stderr, global/help/output errors                                              | One human diagnostic when structured output cannot be emitted or parsing is global; no duplicate emoji/error line after JSON            |
| `workspace.json`, `runtime/binding.json`, lock files                           | Ownership/current state, not diagnostic logs; native evidence is revalidated                                                            |
| `agents/*/{draft,inbox,read,sent,failed}/*.md`                                 | Complete durable mail for its participants; independent copies and stable IDs, never diagnostic truncation                              |
| `read/*.pop.json`                                                              | Minimal successful-claim receipt and actual claim time; empty POP never rewrites it                                                     |
| `draft/*.unknown.json`                                                         | Unavailable interface delivery preserves a complete candidate and reason without a replay queue or confirmed-failure claim              |
| `failed/*.failure.json`                                                        | Confirmed non-delivery reason, separate from uncertain input or task failure                                                            |
| `transactions/*.json`                                                          | Bounded interrupted file-operation intent; remove only on completed recovery, never run commands or enqueue notifications               |
| `runtime/notices/*.json`                                                       | Per-message input attempt/receipt prevents replay; accepted/unknown are separate from POP                                               |
| `runtime/notice-locks/*`, `runtime/input-guards/*`                             | Per-recipient serialization and pending input evidence survives process death; current state can be updated, no queued delivery service |
| `runtime/observations/s-*.json`                                                | Immutable native text and coverage, private; no role/model identity invention                                                           |
| `runtime/observations/index.json`, capture lock                                | Bounded current pointers and generation fencing; history is never rescanned or pruned for status                                        |
| `agents/*/executions/<operation>.json`                                         | Minimal invocation, pinned parties, decision and outcome; never a reusable grant or automatic retry                                     |
| execution `decisions/` and `waiters/`                                          | Immutable reserved choice and exact waiter metadata; waiter authority additionally requires its live OS lock                            |
| Private shell source/PTY/pipes                                                 | Transient execution plumbing, cleaned after completion; no permanent stdout/stderr files                                                |
| Fixture reports, input traces and transcripts                                  | Development evidence only, at the printed owned temporary paths; no production model server or second diagnostic destination            |
| `--debug`, `--verbose`, `--log-file`, daemon event logs                        | No reachable knobs or emission path retained; no new log-level or full-output platform                                                  |
| Former related-capture receipts, leases and pruning                            | Remove code and automatic deletion; historical user files are left untouched                                                            |
| `registry/workspaces.json`, `journal/events.jsonl`, fake pane/agent registries | Do not create unused infrastructure; current workspace/native binding plus necessary mail/audit records supply the required facts       |

Workspace directories are 0700 and records 0600, with bounded private-file
readers and symlink refusal. Mail scans admit at most 2048 entries per mailbox;
sidecars consume scan entries but are not message counts. Oversized/damaged
boxes are unknown with a reason, without deleting history to satisfy the bound.
Per-record JSON is at most 64 KiB; mail/recovery encoding has a tighter 60-KiB
bound. Execution audit/request encoding may reject a large escaped source before
delivery or execution. Decision reasons are 1–4096 bytes. There is no automatic
expiry, count pruning, rotation or external cleanup owner. The user deletes
saved logs, snapshots and mail explicitly.

Session capture selects once, up to 64 panes; stores at most 30 recent lines and
8 KiB of valid UTF-8 per pane; bounds each native frame probe to 200 ms and
screen output to 60 KiB; and limits capture to one second plus 500 ms
persistence. Coverage records distinguish enumeration failure,
skipped/dead/linked panes and partial results. Sequence/generation fencing
prevents stale completion from replacing a newer index. A first or replaced
native pane has no inferred change time. Snapshots are sequential observations,
not one simultaneous screen image.

Shell initialization has a 15-second acknowledgement bound. Normal user startup
and environment-loader trust rules apply; Postman never approves `.envrc` for
the user. Initialized command stdin is a private PTY without input forwarding;
stdout/stderr are non-TTY streams. Bash and Zsh are verified; POSIX-compatible
shells use compatible source/control syntax. Fish/Nu and arbitrary shell
grammars fail explicitly rather than silently running source in another shell.
