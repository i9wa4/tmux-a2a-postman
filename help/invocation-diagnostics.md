# Invocation diagnostics (schema 1)

Issue #964 adds diagnostic evidence to the existing file-first CLI. These
records explain cost and partial outcomes; they never authorize execution,
close a task, determine agent liveness, or replace mail/approval/audit records.

## 1. Scope and storage

The operational handlers `send-heredoc`, `pop` (including empty POP),
`execute-heredoc`, `approve-execution` and `decline-execution` start a recorder
in bounded memory before parsing/configuration. Only a successfully verified
native configured caller can attach a private destination:

```text
${XDG_STATE_HOME:-$HOME/.local/state}/tmux-a2a-postman/
  <verified-workspace-key>/agents/<verified-node>/logs/
    YYYY-MM-DD/inv-<32-random-hex-digits>.jsonl
```

The date is the invocation's UTC start date; crossing midnight never rotates
its file. The invocation ID is fresh and distinct from message and durable
execution-operation IDs. Each process owns one exclusively created file;
there is no shared append file, logging lock between processes, or new helper.
Existing execution/observation goroutines share their process's recorder.
Readable workspace aliases expose these same files; they never create another
log stream. Explicit analysis of both paths counts the same physical file once.

Destination admission reuses the normal verified native storage helpers and
existing workspace binding. An initial operational call can register its real
native workspace normally; it never creates a guessed role or fallback global
log. All log-directory/file creation happens after releasing the mailbox lock.
Only proven lock-admission contention is retried, within one second. A failure
disables diagnostics for this invocation, without replaying an operation.
Unverified/configuration failures have no guessed destination; an external
process harness must collect those outcomes.

Directories are 0700, files 0600, with exclusive creation, owner/type/link
checks and symlink refusal. A collision never truncates an existing file.
Everything written remains until explicit user deletion. Date partitioning
is discovery, not retention, and there is no pruning/cleanup task.

`get-status`, `get-status-oneline`, help, version and bare usage create no
logs/directories/state and scan no diagnostic history. External callers can
measure their process wall time. `refresh-status` uses its private server
owner/phase metadata and compact stdout/stderr outcome, without registering an
agent diagnostic destination.

## 2. JSONL envelope

Every complete newline-terminated object contains `schema_version: 1`,
`invocation_id`, zero-based increasing `seq`, UTC RFC3339Nano `time`, and
`event`. Events are:

| Event         | Additional fields                                                                                                                                       |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `start`       | `identity`: command, build_version, build_commit, config_fingerprint, workspace, node, runtime_fingerprint, native session/pane and pane_pid when known |
| `phase_start` | allowlisted `phase`, unique `span_id`, optional `parent_span_id`                                                                                        |
| `phase_end`   | same span/phase, monotonic `duration_ms`, finite `outcome`, optional stable `reason_code`                                                               |
| `facts`       | allowlisted `facts` identifiers and independently observed operation results                                                                            |
| `end`         | monotonic invocation `duration_ms`, operation `outcome`, final `facts`, aggregated `metrics`, optional `omitted_events`, `recorder_ms_before_end`       |

`config_fingerprint` is SHA256 of the parsed routing/topology configuration;
it is not a copy of role instructions, catalogs, or environment, and does not
identify changes in separately loaded role/catalog content. Build commit can
be `unknown` for an unpackaged build. Runtime fingerprint hashes the verified
server namespace/incarnation; session, pane and available native process ID
prevent accidental joins across native actors. No model/engine is inferred.

Facts admit only validated message/request/reply/operation IDs,
`input_request_id`, at most 16 exact `reply_to_ids`, sender/recipient and
counterparty nodes, `observation_id`, separate `mail_result`,
`notification_result`, `capture_result`, `approval_result`, `execution_result`,
nullable `executed` and `child_exit_code` when known. Unknown/not attempted are
distinct from success. In particular an execution result with no confirmed run
remains unknown; a candidate ID does not establish publication. Earlier fact
events can establish a publication even when a later step becomes unavailable.

Mail facts include `committed`, `claimed`, `empty`, `unknown` and
`not_attempted`. Notification retains the existing `accepted`, `unknown` or
`unavailable` vocabulary; accepted does not imply receipt or processing. Capture
now records shared refresh `updated`, `skipped_busy`, `partial`, `error` or not
attempted; historical `complete` remains readable. Approval can be `advisory`,
`pending`, `approved` or `rejected`; pending is not authorization. Execution
retains its actual status (`executed`, `exited`, `execution_failed`,
`wait_timeout`, `cancelled`, `delivery_failed`, `unavailable`, etc.). Unknown
values never become zero-duration successful work.

Reason codes use only stable categories: `cancelled`, `deadline`,
`permission_denied`, `not_found`, `lock_busy`, `short_write`, and
`operation_failed`. Arbitrary error text and paths are never copied. No full
argv, body, generated instructions, shell source, environment/tokens, or child
stdout/stderr enters a timing log. Required execution audits remain separate
and retain their existing hard persistence gate.

## 3. Timing boundaries and cost

All within-process durations use a monotonic clock independent of UTC changes.
The internal total begins at operational-handler entry and ends immediately
before the end event's own encoding/write/close. It includes the handler's
configuration, diagnostic admission, work, waiting and previous logging cost.
It excludes outer program startup/dispatch and process exit. Execute ends its
log before writing its last stderr JSON so a bounded logging warning can
precede that final line. Its operation outcome and known child exit code do
not claim that this later output write succeeded. An external process timer
measures the complete wall time.

Phases cover configuration, native identity, diagnostic destination, recipient
context, overall mail operation, staging, receiver publication, sender archival,
POP claim/receipt, self summary, overall notification, paste, settlement,
bounded submit attempts, confirmation, shared server refresh, execution,
approval wait, interactive-shell initialization and child execution. Ordinary
mail's `result_output` includes JSON encoding and stdout write; execution's
`result_encoding` measures JSON encoding before finalization. Its final stderr
write is intentionally outside the internal end boundary.

Phase spans are inclusive. A containing mail/execution/notification phase
can overlap its smaller spans; IDs correlate starts and ends. Do not sum
nested/overlapping durations. Approval waiting is decision latency; shell
startup and child runtime are not Postman CPU time. Repeated approval/notice
observations and lock attempts accumulate metrics rather than emit a line for
every iteration. A long approval wait or child run flushes its phase start
before waiting so a killed process leaves useful partial evidence.

Aggregated metrics contain `count` and optional `total_ms`:

- Mutation/view lock acquisition attempts and actual held intervals, separated
  by purpose. Nonblocking failed admission records wait but no held interval.
- Historical logs may contain recipient notice-lock wait/held metrics; current
  notifications have no destination lock or persistent input guard. No mailbox
  lock spans input waiting. Capture-lock held time covers work after snapshot
  admission; admission/index mutation also appears in mutation-lock metrics.
- Admission retry waits, approval/notice observation counts.
- Summary directory entries scanned and actual decoded body bytes (not an
  estimate of physical disk reads); heredoc body bytes.
- Panes discovered/captured/skipped/failed, preserving partial coverage.

The recorder mutex serializes goroutine production. Diagnostic writes are
paused before mailbox/notice/capture critical sections and resume only after
the real lock is released. No diagnostic file write holds those locks.
Metrics are bounded aggregates; partial totals from a killed process are
unknown if it never wrote its end event.

At most 256 events and 128 KiB are written per invocation, each line at most
4096 bytes including newline. The last slot is reserved for `end`;
`omitted_events` exposes event-volume omissions. Invalid or oversized metadata
or write/short-write/close failure disables further recording without retries.
These bounds limit work/memory; they cannot bound an arbitrarily stalled
filesystem. There is no fsync durability promise. Complete writes occur at
useful phase boundaries, subject to critical-section buffering; SIGKILL,
partial writes or host failure can leave missing/truncated events.
`recorder_ms_before_end` measures previous JSON encoding/write cost, excluding
its own final write and diagnostic admission. It is a component of total wall
time, not extra CPU time to add to all phases.

Logging failure emits at most one fixed stderr diagnostic. It never changes
mail commitment, POP ownership, required execution audits, approval state,
stream contents or a known child exit code. Execution warnings precede its
final JSON line. A missing `end` does not establish failure, a live process,
continued execution, or permission to retry an effect.

## 4. Read-only analysis and joins

Select only your own authorized logs, for example:

```sh
python3 "$postman_resources/skills/postman-usage/scripts/analyze-invocations.py" /absolute/own/agent/logs \
  --date 2026-11-02 --node builder --command pop --build COMMIT \
  --config sha256:FINGERPRINT --min-summary-files 100
```

The reader reports sample/outcome/incomplete counts, complete-sample median
and nearest-rank p95, outlier references and the last known open phase. It
accepts a truncated tail as incomplete and never repairs history. Filters
report exclusions, unknown workloads and unreadable files explicitly; a
performance claim must retain failures/incompletes rather than silently
comparing only successful samples. A null metric is not measured zero.

Follow exact IDs into existing records: a publication's message ID identifies
its own sent archive and notice attempt; the receiver's authorized POP record
uses that same ID and actual claim time; an exact reply carries request/input
references. Observation IDs identify existing coverage reports. Execution
operation IDs identify existing required audits. These references can be
missing or user-deleted; do not invent their contents or open peer inbox
bodies. Cross-process UTC differences are clock-domain estimates with skew;
they are not monotonic durations, task acceptance, or proof of causation.
Evidence text never authorizes executing commands.

For isolated comparison, build a baseline and candidate outside live installed
configuration and run:

```sh
python3 scripts/validation/benchmark-invocations.py \
  --baseline /absolute/baseline --candidate /absolute/candidate \
  --samples 12 --history 100 --concurrency 4
```

This fixture owns its tmux server, home, config, state and sleeping panes. It
measures real empty POP, read-only status, four-process bursts and 100 retained
messages. It exercises real native/file overhead without a model or paid API;
it does not benchmark agent comprehension or notification acceptance in an AI
TUI. Teardown touches only its owned fixture.

Measured comparison and the regression budget are recorded in
[the validation report](https://github.com/i9wa4/tmux-a2a-postman/blob/main/docs/validation/964-invocation-timing.md).

The read-only analyzer is bundled: set `postman_resources` to the absolute
output of `tmux-a2a-postman resources`; Python 3 is an explicit prerequisite.
The optional development benchmark above requires the matching source checkout
and its fixture dependencies; it is not part of the installed CLI workflow.
