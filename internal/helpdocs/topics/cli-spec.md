# CLI specification

This is the canonical input/output contract for the daemonless CLI agreed in
[issue #937](https://github.com/i9wa4/tmux-a2a-postman/issues/937).
Command and workflow HelpDocs have one Markdown editing source embedded in
the binary. Stable thin Skills select the relevant installed help topic.

The optional external binding adds `channel-sync` (no command arguments) and
`channel-report-heredoc --to <one-claimed-external-id> --state <done|blocked|pending>`
(quoted heredoc stdin). Pending requires `--scope <action-context-SHA256>`;
terminal states forbid scope. Both accept common `--config`/help and reject
duplicate options. Their `tmux-a2a-postman help external-filesystem-channel`
defines provisioning, literal file input, exact decision filenames, bounded
foreground activation, idempotency and catch-up. JSON returns success/as_of and
reason on failure; sync includes accepted/published/pending/more, while report
includes message_id/exchange_id and the local-publication boundary. Partial
durable results survive errors. These commands require the configured top-level
interface; ordinary send/POP shapes remain unchanged. Status follows §3.

## 1. Commands and common rules

| Command              | Purpose                                                     |
| -------------------- | ----------------------------------------------------------- |
| `launch-agents`      | Explicitly create one preset team in a dedicated session    |
| `refresh-status`     | Collect one finite shared server snapshot                   |
| `get-status`         | Read the saved explicit-server JSON snapshot                |
| `get-status-oneline` | Render the same saved server snapshot as one human line     |
| `pop`                | Claim one own unread mail and return its final archive path |
| `send-heredoc`       | Send a request or reply to claimed message IDs              |
| `execute-heredoc`    | Review and execute source in the caller's shell environment |
| `approve-execution`  | Send approval for one claimed execution request             |
| `decline-execution`  | Send rejection for one claimed execution request            |
| `help [topic]`       | Explain commands without initializing workspace state       |
| `version`            | Print name, version and commit as JSON                      |
| `skills`             | List or display the complete thin entry-point Markdown      |
| `resources`          | Explicitly materialize the offline helper bundle            |

`help` and command help read Markdown directly without configuration, tmux,
credentials, extraction or runtime mutation. `skills list` and `skills show`
are also read-only; the consumer's Skill manager owns installation and updates.
`resources` returns a validated absolute, content-identified cache path
as plain text. See `tmux-a2a-postman help skills` and
`tmux-a2a-postman help resources` for their complete independent interfaces.

Operational commands accept `--config <absolute-postman.md>` and `--help`.
Without an explicit override, the inherited absolute
`TMUX_A2A_POSTMAN_CONFIG` selection takes precedence over
`$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md`, or
`$HOME/.config/tmux-a2a-postman/postman.md` when XDG_CONFIG_HOME is unset.
The top-level `--config` selects the same configuration. Relative or duplicate
options, unexpected positional arguments and removed options are errors.
`--postman-md` is removed from the product CLI. The notification test driver's
similarly named fixture-copy option is a separate development interface.

`launch-agents <preset>` is an optional finite operation with `--socket`,
`--cwd`, `--session`, and inert `--dry-run`; `launch-agents --list` lists
validated presets without creating anything. It accepts one positional preset;
the mail/status commands retain their argument contracts. See the
`tmux-a2a-postman help launching` for exact selection, shell initialization,
config inheritance, native ownership, partial starts, and cleanup. It never
injects initial mail or supervises consoles. Other commands never invoke it.

The workspace value is the canonical HOME-relative identity (`~` or `~/path`).
The caller comes from verified native tmux identity. The workspace directory is
the verified agent pane's native `pane_current_path`, canonicalized below HOME.
It is independent of the session's old creation directory and the CLI
subprocess's cwd. Only configured live role panes in that same canonical
directory supply local bindings. A present role elsewhere or with an unknown
directory is not proven absent; a configured approver in that state cannot
permit advisory execution. Execution uses its explicit, captured tool cwd.
Actual changes to the agent pane's own directory select a different workspace;
ordinary tool subprocess cd does not. No launcher-specific setup is required.
Runtime replacement preserves durable mail. Existing files under an older
session-directory workspace remain unchanged; this correction never migrates or
deletes them automatically. Inspect the original records explicitly when
recovering earlier mail. Unknown ownership never grants permission to mutate or
execute. No daemon startup, registration, model API or recurring POP loop is
required.

Except for Markdown help, the `resources` path, execution's child streams and
the human `get-status-oneline` view,
outputs are JSON. Times are UTC RFC3339.
`as_of` is when mail facts were aggregated; it is not a capture timestamp.
Missing or unverifiable values are `null` with a useful reason, never invented
zero counts. A failed supplemental observation does not undo a committed send,
claim, decision or completed command.

## 2. Common node summary

Both an attributed saved pane `status` and operation `self` use these fields:

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

Both getters read one saved whole-server snapshot. `get-status` returns the
exact JSON bytes, preserving saved `as_of`; `get-status-oneline` renders that
same document with the current clock for glyph aging. No native tmux query,
configuration load, mailbox scan, mkdir, lock or persistent diagnostic write
occurs. Missing/malformed/incompatible cache is explicitly unavailable/nonzero;
valid stale evidence remains dated. A read never verifies live incarnation.

`refresh-status` explicitly collects one finite snapshot across native sessions,
windows and panes. It is also reused in process after eligible normal
operations. One nonblocking advisory lock returns `skipped_busy` immediately;
partial/error outcomes are nonzero, retain actual capture times and explain
failure. No daemon, timer or hidden refresh occurs. External status integration
must refresh then render even when refresh fails; preserve stderr diagnostics
outside the bar.

All three commands require explicit `TMUX=absolute-socket,PID,index`; PID 0 is
unknown. Split at the last two commas. A known PID mismatch is rejected. They
need no role/TMUX_PANE. `--config` is accepted but not read and cannot select
server membership. Configuration attribution comes from provenance saved in
verified normal operations, with conflicting/changed inputs explicitly unknown.

The versioned document has `server`, `started_at`, `as_of`, `coverage` and
`sessions[].windows[].panes[]`. Native IDs/names/layout/processes are retained.
Each pane has capture/status/scope (nullable); status uses §2's node fields,
scope records configured edges/interface/session relations. Select the assigned
server, native session/pane and workspace before interpreting it. Operator-wide
visibility never grants authority to manage/send/read every visible mailbox.
There is no top-level workspace/nodes/parent/children projection.

See `tmux-a2a-postman help server-status` for schema,
storage, attribution, failure/lock boundaries and the final writer/call-site
map.

### 3.1. get-status-oneline

Example: `[0]🟩💛:🟢 [3]⬛🟥 [9] [12]⚫`. `[N]` is native `$N`, numerically
sorted, including gaps/two-digit IDs, independently of session names. Colons
separate included agent windows, with panes in physical top/left order.
Ordinary panes have no glyph; their native session label remains visible.
Missing/incompatible cache emits `?` and exits nonzero, with stderr diagnostics.

Shape uses the shared node summary: unread or inbound work produces a square,
including when outbound waits also exist; otherwise an outbound wait produces
a heart; otherwise all three zero counts produce a circle. All three counts
must be known and nonnegative; otherwise the glyph is `?`. Draft and failed
counts do not affect the palette. A circle does not assert task completion.

| Last observed screen-change age | Circle | Square | Heart |
| ------------------------------- | ------ | ------ | ----- |
| 0–<60 seconds                   | 🟢      | 🟩      | 💚     |
| 60–<300 seconds                 | 🟡      | 🟨      | 💛     |
| 300–<900 seconds                | 🟠      | 🟧      | 🧡     |
| 900 seconds or more             | 🔴      | 🟥      | ❤️    |
| Unknown activity                | ⚫      | ⬛      | 🖤     |

Color requires a capture less than 120 seconds old, independently of those
age bands. At exactly 120 seconds it becomes black. A missing comparison
baseline, invalid/future/inconsistent timestamps or a changed native pane
also makes activity unknown. A fresh unchanged capture can retain a red
screen-change age. Red describes old observed movement, not failure or a
stopped process. Equal samples do not prove continuous inactivity; changed
screens may be spinners or pasted input. Last POP is never an activity fallback.

Configured titles alone grant no identity. Known summaries require matching
saved bindings/config provenance and collection-time native process identity.
Unknown/partial rows retain reasons on stderr. Current-clock colors do not
update the saved collection or individual capture timestamps.

## 4. pop

```sh
tmux-a2a-postman pop
```

No command-specific options or positional arguments are accepted. One eligible
own inbox message is exclusively moved to read and receives a durable receipt.
The existing selection order is preserved; random message IDs are not a FIFO
timestamp contract. Output fields are `workspace`, `as_of`, `success`,
`markdown_absolute_path` and common `self`.

A non-null path also returns `instruction`, explicitly requiring a complete
read of that Markdown through EOF before processing, replying or deciding that
no action is needed. A truncated view, summary, notification, metadata or reply
guidance does not substitute for the full file. Empty POP omits this field.

`self.unread_count` counts messages remaining after this POP, excluding the
claimed message. A known positive count is also named in `instruction`: fully
read and handle this archive, then POP the next mail without waiting for another
notification. Continue only while the latest operation reports a positive
count. Zero, unknown or an empty POP ends the batch; never poll an empty inbox.

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

Each new archive retains generated recipient context before the literal sender
body: identity/correlation, complete common/role policy, ordinary configured
skill catalog metadata, recipient-viewpoint contacts with concise role
summaries, and exact current reply actions. A visible Sender Message boundary
separates the generated content from unchanged sender bytes. Exact replies
retain the same context but create no new obligation; typed execution requests
use fixed decision actions. Ordinary configured footer guidance is preserved
where applicable. See
`tmux-a2a-postman help recipient-markdown`.

The archive header stores a versioned context byte count and SHA256 descriptor;
durable intents retain the full generated text. Recovery keeps that exact
context. Legacy archives remain readable. Context is at most 48 KiB, catalogs at
most 256 entries/32 KiB, durable intents at most 60 KiB and complete archives at
most 64 KiB. Exceeding a bound fails rather than truncating role instructions.

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

The foreground sender pastes one short hint once. After at least 600ms it
captures the whole pane and sends C-m; after another 600ms it captures again.
A bounded line diff counts inserted/deleted lines, ignoring trailing terminal
padding. A replacement counts as two lines; a one-row scroll does not make
every subsequent row different. At most four changed lines is small enough
to retry Enter, with at least 600ms between presses and a maximum of three.
Substantial change stops extra Enter. No prompt glyph, cursor prefix, input
area, provider name or model API is inspected. An identical screen can mean
newline or accepted input; the retry heuristic deliberately permits extra Enter
and never establishes recipient acceptance or processing. `accepted` retains
its transport-only compatibility name for a substantially changed screen;
`unknown` includes exhausted small-change retries. POP/full reads/exact replies
and execution effects require their own evidence.

Independent messages do not acquire a destination notification lock or
read/write persistent input guards. Concurrent senders can paste complete hints
before the last process submits them. There is no last-sender registry, queue,
replay worker or daemon. Per-message attempt reservation and short workspace
mailbox locks still prevent duplicate paste/claim and protect durable mail.
Uncertain attempts are not re-pasted. Historical guard/lock files remain
untouched and are ignored. Human drafts and unrelated terminal writers remain
outside this Postman transport's control.

Paste, waits, Enter retries and synchronous native captures have a five-second
input budget within the eight-second total post-commit deadline. Every key
revalidates native ownership and shell/tmux-mode rejection. Observation timeout
may retry only the read within that budget; actual native identity/geometry
failures stop input immediately. Receipt persistence uses the remaining outer
budget and may retry only its idempotent transition, never paste or Enter.

Eligible send/POP (including empty POP), decisions, execution and external
operations attempt the same bounded whole-server refresh after primary locks.
Busy skips immediately. Primary operation success, IDs, recovery and verified
operation-local self remain authoritative even if observation fails.
Notification confirmation still uses synchronous live recipient-only safety
captures; it never uses the cache or waits for its lock. See §3's contract for
cadence, coverage, raw history retention, limits and exact call sites.
Logs/snapshots remain until manually deleted; no automatic expiry or pruning
occurs.

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
Dispatch-audit lock admission waits at most ten seconds outside the lock, also
bounded by the original approval/caller deadline. A finite wave of concurrent
POP/capture mutations must not discard an otherwise approved invocation after
only two seconds. Admission failure still prevents execution.

Stdout/stderr are streamed, not permanently logged by Postman. The final JSON is
a distinct last stderr line with common workspace/as_of/self, success, executed,
exit_code and approval facts. executed=false means confirmed not run;
executed=null means possibly started with unknown outcome. Preserve a known
command exit code; wrapper failures are nonzero and structurally
distinguishable. Approval reason and system failure reason are separate. Never
automatically repeat an interrupted possibly started execution. Audit is minimal
outcome and correlation evidence, not a reusable grant or full output-retention
platform.

An unknown execution outcome includes `recovery_operation_id`, identifying
`agents/<self.node>/executions/<id>.json` under this workspace's state
directory. Use that exact audit and the command's own evidence to investigate;
this is not permission to run it again. Normal completed results omit this
diagnostic ID.

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

| Surface                                                               | Decision and purpose                                           | Callers and rationale                                                                    |
| --------------------------------------------------------------------- | -------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `get-status`                                                          | Saved server snapshot; explicit refresh-status owns collection | Agents/operators need one saved-file view; no global workspace scan                      |
| `pop [id]`                                                            | Simplify to argument-free `pop`                                | Receivers claim their actual next mail; notification IDs remain hints                    |
| `send-heredoc --to node`                                              | Keep; new requests require replies                             | Senders need explicit configured routes and literal stdin                                |
| `--to session:interface`                                              | Keep direct parent/child routes                                | Interface nodes need exact per-hop identity without foreign capture access               |
| `--reply-to`, `--fills-input-request-id`                              | Remove from public input; derive from `--to <POP'd-id>`        | Agents should not transcribe correlation metadata                                        |
| Comma-separated `--to` IDs                                            | Add bounded combined replies, maximum 16                       | One answer can resolve several claimed requests with one sender/route                    |
| `--reply-required`, `--no-reply`                                      | Remove; request/reply semantics determine policy               | Avoid contradictory reply flags and endless reply obligations                            |
| `execute-bash --command`                                              | Replace with `execute-heredoc --reason` and stdin              | Exact multiline source, cwd and initialized shell form one reviewable request            |
| `approve-execution`, `decline-execution`                              | Add fixed actions with `--to` and `--reason`                   | Approvers choose one action without JSON/code copies or boolean ambiguity                |
| Decision/helper stdin                                                 | Reject nonempty stdin                                          | Reasons and target are explicit flags; source is never copied by the approver            |
| `--config`                                                            | Keep one absolute override, before or after the command        | Operators/fixtures need explicit topology; defaults are cwd-independent                  |
| `--postman-md`                                                        | Remove product alias                                           | One spelling for one input; the smoke driver's fixture-copy flag is separate             |
| `--capture-related-panes`                                             | Remove; valid send/POP always trigger bounded session capture  | Visibility should not depend on each agent remembering a flag                            |
| `help [topic]`, `<command> --help`, top-level `--help`                | Keep without runtime state; reject extra help arguments        | Markdown HelpDocs own syntax and procedures; thin Skills select installed topics         |
| `version`, top-level `--version`                                      | Keep compact name/version/commit JSON                          | Operators need the actual installed revision                                             |
| Bare executable                                                       | Keep usage display, no mutation                                | No implicit startup operation                                                            |
| Duplicate/unknown flags and extra positionals                         | Reject before mail publication/execution                       | No last-value-wins decisions or hidden compatibility branches                            |
| HOME and XDG_CONFIG_HOME                                              | Keep absolute, canonical ownership/config sources              | Agents/operators use normal XDG layout; relative values fail                             |
| XDG_STATE_HOME                                                        | Keep absolute private state selection                          | Tests and operators isolate mail; no new root flag                                       |
| TMUX, TMUX_PANE and tmux native evidence                              | Keep verified identity inputs                                  | Mutable names, cwd alone and PID liveness alone are insufficient                         |
| tmux default-shell, cwd and exported caller environment               | Keep explicit execution environment                            | Fresh shell configuration and first-prompt hooks run after authorization                 |
| Legacy TOML, POSTMAN_HOME, context/root/daemon overrides              | Remove/ignore as authority                                     | They must not gate or redirect current mail                                              |
| Daemon start/stop/RPC, observer, model adapters, grants/resume policy | Remove                                                         | No resident process, retry service or second state engine                                |
| Unused related capture-registration ledger                            | Remove dormant code                                            | Deferred state-machine work must not masquerade as current authority                     |
| Pure compaction-marker helper                                         | Keep isolated, without automatic observation or action         | Preserve reusable parsing without coupling mail/notification to a model                  |
| `nix run .#notification-smoke` and Python smoke options               | Keep development-only                                          | Fixture/local-model/live modes, case/terminal selection, reports and config-copy testing |
| Nix update-toolchain, skill-check/publish and release helpers         | Keep explicit development/release tools                        | They do not add Postman operational commands; publishing remains user/release-owned      |

### 10.2. Outputs

Before, send returned `status`, a full metadata envelope and a separate notice;
POP returned metadata, archive and receipt paths; self mixed obligation arrays,
peer guidance, next-action and activity labels. These are replaced with the
common counts/timestamps documented above. The full immutable mail remains in
its archive. A notification never exposes the body.

| Output                                                              | Decision and reason                                                                                     |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `workspace`, `as_of`, operation `success`, common `self`            | Keep compact context and operation result on every mail outcome                                         |
| `nodes`, interface `parent`/`children`                              | Removed projection; saved pane status/scope and declared relations replace it                           |
| `markdown_absolute_path`                                            | Keep only POP's final archive path; empty is null                                                       |
| Metadata/body/receipt echo, old status labels, next-action/activity | Remove redundant or inferred fields; archives and file facts are authoritative                          |
| `notification`                                                      | Show only unavailable/unknown; never reverse delivery success                                           |
| `reason`, `recovery_message_id`                                     | Keep failure diagnosis and stable recovery identity without a resend instruction                        |
| `recovery_operation_id`                                             | Show for unknown execution outcomes to locate the exact audit without authorizing a retry               |
| Null counts/times                                                   | Keep unknown distinct from zero; diagnostics cannot change operation success                            |
| Child stdout/stderr and exit                                        | Keep distinct streams; only final execution JSON/progress uses stderr                                   |
| `executed`, `exit_code`, `approval`                                 | Separate run/no-run/uncertain outcome and reviewer's reason from system failure                         |
| Emoji error prefix and repeated command error text                  | Remove after structured errors; retain a single diagnostic for global/unknown commands or broken output |
| Help/error workflow dumps                                           | Keep scoped command and workflow topics in embedded canonical Markdown; thin Skills point to them       |

### 10.3. Every log and persisted record destination

Operational `send-heredoc`, `pop` (including empty), `execute-heredoc`,
`approve-execution` and `decline-execution` also write best-effort private
per-invocation diagnostics after verified native identity. These logs never own
mail or execution recovery. Read-only/status/help/version/usage commands create
no logs. See the
`tmux-a2a-postman help invocation-diagnostics` for
boundaries, privacy, limits and measured cost.

| Destination                                                                    | Purpose, audience and decision                                                                                                                                                |
| ------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| stdout, operational CLI                                                        | One compact JSON result for agents, except the human get-status-oneline view; no banners, raw mail or debug prose                                                             |
| stdout, execution child                                                        | Exact child stream for user pipelines; no Postman prose                                                                                                                       |
| stderr, execution                                                              | Exact child stream plus short waiting notice and final JSON line; add only a necessary line boundary                                                                          |
| stderr, global/help/output errors                                              | One human diagnostic when structured output cannot be emitted or parsing is global; no duplicate emoji/error line after JSON                                                  |
| `workspace.json`, `runtime/binding.json`, lock files                           | Ownership/current state, not diagnostic logs; native evidence is revalidated                                                                                                  |
| `agents/*/{draft,inbox,read,sent,failed}/*.md`                                 | Complete durable mail for its participants; independent copies and stable IDs, never diagnostic truncation                                                                    |
| `read/*.pop.json`                                                              | Minimal successful-claim receipt and actual claim time; empty POP never rewrites it                                                                                           |
| `draft/*.unknown.json`                                                         | Unavailable interface delivery preserves a complete candidate and reason without a replay queue or confirmed-failure claim                                                    |
| `failed/*.failure.json`                                                        | Confirmed non-delivery reason, separate from uncertain input or task failure                                                                                                  |
| `transactions/*.json`                                                          | Bounded interrupted file-operation intent; remove only on completed recovery, never run commands or enqueue notifications                                                     |
| `runtime/notices/*.json`                                                       | Per-message input attempt/receipt prevents replay; accepted/unknown are separate from POP                                                                                     |
| `runtime/notice-locks/*`, `runtime/input-guards/*`                             | Historical only: retained untouched, never read/written or used to block current notification input                                                                           |
| `runtime/observations/s-*.json`                                                | Immutable native text and coverage, private; no role/model identity invention                                                                                                 |
| `runtime/observations/index.json`, capture lock                                | Historical files retained; no current writer; server %status snapshot/history replaces them                                                                                   |
| `agents/*/executions/<operation>.json`                                         | Minimal invocation, pinned parties, decision and outcome; never a reusable grant or automatic retry                                                                           |
| `agents/<verified-node>/logs/YYYY-MM-DD/inv-<random-id>.jsonl`                 | Versioned timing/outcome/correlation diagnostics; invocation UTC start-date partition, 0700/0600, no expiry, no content/stream copies; recording failures never grant retries |
| execution `decisions/` and `waiters/`                                          | Immutable reserved choice and exact waiter metadata; waiter authority additionally requires its live OS lock                                                                  |
| Private shell source/PTY/pipes                                                 | Transient execution plumbing, cleaned after completion; no permanent stdout/stderr files                                                                                      |
| Fixture reports, input traces and transcripts                                  | Development evidence only, at the printed owned temporary paths; no production model server or second diagnostic destination                                                  |
| `--debug`, `--verbose`, `--log-file`, daemon event logs                        | No reachable knobs or emission path retained; no new log-level or full-output platform                                                                                        |
| Former related-capture receipts, leases and pruning                            | Remove code and automatic deletion; historical user files are left untouched                                                                                                  |
| `registry/workspaces.json`, `journal/events.jsonl`, fake pane/agent registries | Do not create unused infrastructure; current workspace/native binding plus necessary mail/audit records supply the required facts                                             |

An existing application state root may remain 0755; it must be an owned real
directory without group/other write access. Postman preserves its permissions.
Workspace/lock directories are 0700 and records 0600, with bounded private-file
readers and symlink refusal. Mail scans admit at most 2048 entries per mailbox;
sidecars consume scan entries but are not message counts. Oversized/damaged
boxes are unknown with a reason, without deleting history to satisfy the bound.
Per-record JSON is at most 64 KiB; mail/recovery encoding has a tighter 60-KiB
bound. Execution audit/request encoding may reject a large escaped source before
delivery or execution. Decision reasons are 1–4096 bytes. There is no automatic
expiry, count pruning, rotation or external cleanup owner. The user deletes
saved logs, snapshots and mail explicitly.

Server refresh has a two-second total budget, one-second/64-pane capture sweep,
200ms/60-KiB native frame bound and 30-second per-pane cadence. Raw captures
retain up to 30 lines/8 KiB; partial sweeps preserve individual times and keep
uncaptured panes due. Atomic publication and one nonblocking stable-inode lock
replace generation/index writers; see the
`tmux-a2a-postman help server-status`.

Shell initialization has a 15-second acknowledgement bound. Normal user startup
and environment-loader trust rules apply; Postman never approves `.envrc` for
the user. Initialized command stdin is a private PTY without input forwarding;
stdout/stderr are non-TTY streams. Bash and Zsh are verified; POSIX-compatible
shells use compatible source/control syntax. Fish/Nu and arbitrary shell
grammars fail explicitly rather than silently running source in another shell.

## 11. Result examples

The values below are illustrative; paths, IDs and timestamps depend on the
invocation. Each block is a complete JSON object. Node/self fields have the
same meaning in every command; no field is a working/idle classification.

### 11.1. Saved whole-server status

Illustrative unavailable pane (abbreviated layout text; all schema fields
shown):

```json
{"version":1,"server":{"socket":"/tmp/example/s","canonical_socket":"/tmp/example/s","pid":1234,"incarnation":"1234:1793520000:1:2:1793520000000000000"},"started_at":"2026-11-01T10:00:00Z","as_of":"2026-11-01T10:00:00Z","coverage":{"enumerated":1,"captured":0,"reused":0,"unavailable":1,"unknown_nodes":0,"complete":false,"reason":"capture unavailable"},"sessions":[{"id":"$9","name":"project","windows":[{"id":"@3","index":1,"name":"terminal","layout":"native-layout","panes":[{"id":"%4","pid":"1235","title":"terminal","workspace":"~/work","top":0,"left":0,"dead":false,"linked":false,"capture":null,"status":null,"scope":null,"reason":"capture unavailable"}]}]}]}
```

Ordinary unregistered panes have null status/scope; registered panes use the
node fields in §2 and configuration attribution defined in the server contract.

### 11.2. Successful POP of a request

```json
{
  "workspace": "~/work",
  "as_of": "2026-11-01T10:00:00Z",
  "success": true,
  "self": {
    "node": "worker",
    "unread_count": 0,
    "inbound_count": 1,
    "outbound_count": 0,
    "draft_count": 0,
    "failed_count": 0,
    "last_pop_at": "2026-11-01T10:00:00Z",
    "last_capture_at": null,
    "last_screen_change_at": null
  },
  "markdown_absolute_path": "/home/example/.local/state/tmux-a2a-postman/work/agents/worker/read/m-0123456789abcdef0123456789abcdef.md",
  "instruction": "Read the entire Markdown file at markdown_absolute_path through EOF before processing, replying, or deciding no action is needed. Do not substitute a truncated view, summary, notification, metadata, or reply guidance for the full file."
}
```

### 11.3. Successful empty POP

```json
{
  "workspace": "~/work",
  "as_of": "2026-11-01T10:00:00Z",
  "success": true,
  "self": {
    "node": "worker",
    "unread_count": 0,
    "inbound_count": 0,
    "outbound_count": 0,
    "draft_count": 0,
    "failed_count": 0,
    "last_pop_at": null,
    "last_capture_at": null,
    "last_screen_change_at": null
  },
  "markdown_absolute_path": null
}
```

### 11.4. Delivered request with an uncertain input notification

```json
{
  "workspace": "~/work",
  "as_of": "2026-11-01T10:00:00Z",
  "success": true,
  "self": {
    "node": "worker",
    "unread_count": 0,
    "inbound_count": 0,
    "outbound_count": 1,
    "draft_count": 0,
    "failed_count": 0,
    "last_pop_at": null,
    "last_capture_at": null,
    "last_screen_change_at": null
  },
  "notification": {
    "status": "unknown",
    "pane": "%2",
    "reason": "notification submit is uncertain; no repeat Enter or paste"
  }
}
```

### 11.5. Confirmed non-delivery with a preserved failed message

```json
{
  "workspace": "~/work",
  "as_of": "2026-11-01T10:00:00Z",
  "success": false,
  "self": {
    "node": "worker",
    "unread_count": 0,
    "inbound_count": 0,
    "outbound_count": 0,
    "draft_count": 0,
    "failed_count": 1,
    "last_pop_at": null,
    "last_capture_at": null,
    "last_screen_change_at": null
  },
  "reason": "recipient has no verified local binding",
  "recovery_message_id": "m-0123456789abcdef0123456789abcdef"
}
```

### 11.6. Completed execution with command exit 7

```json
{
  "workspace": "~/work",
  "as_of": "2026-11-01T10:00:00Z",
  "success": false,
  "self": {
    "node": "worker",
    "unread_count": 0,
    "inbound_count": 0,
    "outbound_count": 0,
    "draft_count": 0,
    "failed_count": 0,
    "last_pop_at": null,
    "last_capture_at": null,
    "last_screen_change_at": null
  },
  "executed": true,
  "exit_code": 7,
  "approval": null,
  "reason": "command exited with status 7"
}
```

### 11.7. Declined execution

```json
{
  "workspace": "~/work",
  "as_of": "2026-11-01T10:00:00Z",
  "success": false,
  "self": {
    "node": "worker",
    "unread_count": 0,
    "inbound_count": 0,
    "outbound_count": 0,
    "draft_count": 0,
    "failed_count": 0,
    "last_pop_at": null,
    "last_capture_at": null,
    "last_screen_change_at": null
  },
  "executed": false,
  "exit_code": null,
  "approval": {
    "approver": "reviewer",
    "decision": "rejected",
    "reason": "The proposed operation needs a narrower scope."
  },
  "reason": "approval rejected"
}
```

### 11.8. Interrupted execution with an unknown outcome

```json
{
  "workspace": "~/work",
  "as_of": "2026-11-01T10:00:00Z",
  "success": false,
  "self": {
    "node": "worker",
    "unread_count": 0,
    "inbound_count": 0,
    "outbound_count": 0,
    "draft_count": 0,
    "failed_count": 0,
    "last_pop_at": null,
    "last_capture_at": null,
    "last_screen_change_at": null
  },
  "executed": null,
  "exit_code": null,
  "approval": null,
  "reason": "context canceled",
  "recovery_operation_id": "m-0123456789abcdef0123456789abcdef"
}
```

### 11.9. Successful POP with unavailable supplemental mail facts

```json
{
  "workspace": "~/work",
  "as_of": "2026-11-01T10:00:00Z",
  "success": true,
  "self": {
    "node": "worker",
    "unread_count": null,
    "inbound_count": null,
    "outbound_count": null,
    "draft_count": null,
    "failed_count": null,
    "last_pop_at": null,
    "last_capture_at": null,
    "last_screen_change_at": null,
    "reason": "context deadline exceeded"
  },
  "markdown_absolute_path": null
}
```

Normal send and decision-helper success uses workspace/as_of/success/self
without the POP path or execution fields. A normal accepted notification is
omitted. Supplemental uncertainty does not reverse a confirmed operation.
Execution objects are the final independent stderr line; the exit-7 example
also exits with status 7 and preserves command stdout. A wrapper refusal or
unknown outcome exits nonzero without inventing a child exit code.

The implementation-to-acceptance map and measured capture costs are recorded in
[the completion audit](https://github.com/i9wa4/tmux-a2a-postman/blob/main/docs/validation/937-acceptance.md).
