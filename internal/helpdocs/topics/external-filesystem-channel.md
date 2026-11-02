# External filesystem channel (#767)

## 1. Current implementation and ownership

The adapter reuses native workspace/binding verification, durable mail intents,
inbox/read/sent archives, POP receipts, exact reply references and generic tmux
notification. Historical process-local adapters, phony registries and daemon
supervisors are absent and are not reconstructed. There is no new task database.

The configured top-level interface handles external intake/reports; its
orchestrator owns delegation, and descendants return through parents. #901 is
closed and is not a prerequisite. The adapter shares ordinary notification's
limitation around simultaneous direct human typing: it does not preserve drafts
or arbitrate all conversation writers. Text/filenames do not authenticate a
remote human; the boundary is explicit local provisioning and OS permissions.
Agent relays gain no human authority. Human permission, command review and
runtime sandboxing remain separate.

## 2. Provision one persistent binding

Add these scalar fields to the intended `postman.md` frontmatter alongside the
existing topology and common/role instructions:

```yaml
interface_node: messenger
external_mailbox_root: /home/alice/.local/state/postman-external/project-a
external_workspace: ~/work/project-a
external_session: project-a
```

The workspace is Postman's exact canonical HOME-relative native agent-pane
directory, independent of the CLI subprocess cwd or old session creation root.
The named native session and interface role must match fresh evidence. A
declared child cannot own the channel. Other sessions sharing the configuration
cannot access this binding.

Provision the parent directory. The first scan creates only the final root,
`inbox/`, `outbox/`, `read/`, `binding.json` and `.lock`. Use owned mode 0700
directories and owned regular single-link mode 0600 files. The absolute root
must be canonical without symlink components and separate from internal Postman
workspace storage. Distinct root/workspace/session/node tuples require separate
roots; reusing a provisioned root for another tuple is rejected. Agent/tmux
restart preserves the same binding and files. Nothing is automatically pruned or
reinitialized.

## 3. Producer activation and limits

Write a complete UTF-8 text body to a private temporary file, fsync it,
atomically rename it into `inbox/<stable-unique-name>.md`, then sync the
directory. The body contains no envelope. Names use ASCII
letters/digits/dash/underscore, end in `.md`, and have at most 128 bytes. Input
is 1..32768 bytes without NUL. Published identities are immutable: retries use
the same name and exact text. Non-Markdown staging files and dot-prefixed
temporaries are ignored.

Transport identity folds ASCII filename case so case aliases cannot create a
second request on case-insensitive macOS filesystems. The original spelling is
preserved; changing only its case conflicts rather than importing another
request.

After publication, invoke the foreground command with the configured interface's
actual native context:

```sh
env TMUX='/absolute/canonical/tmux.sock,0,0' TMUX_PANE='%7' \
  tmux-a2a-postman channel-sync --config /absolute/postman.md
```

The invoking integration supplies the intended socket/pane. Postman verifies
their current native role, session, incarnation and canonical workspace; these
environment strings alone are not identity evidence. Run an initial scan on
startup/reconnect and recovery scans after missed events/interrupted calls. An
optional external watcher can invoke the command; Postman requires no resident
watcher or daemon. Without a caller, there is no autonomous intake. Check
failures and retry unchanged identities after fixing their cause. Do not add
empty POP or status polling to conceal a missing producer trigger.

One scan has a 60-second foreground context, at most 16 attention items and a
2048-entry cap per directory. `more=true` means drain the known mail and scan
again. Scan-limit/error results do not establish complete catch-up. Notification
retains its existing bounded post-commit tail.

## 4. Durable intake and crash recovery

Binding/filename deterministically identifies ordinary Postman mail from a
binding-specific external transport role to the interface. Metadata retains the
binding, filename and exchange separately from the literal text. Full common and
recipient instructions are rendered as for agent mail. The transport role is
neither a native agent nor an authenticated remote principal.

Existing recoverable mail intents commit publication and the sent archive before
the exact input is durably copied into external `read/` and removed from
external `inbox/`. Read preserves the original filename and is the consumption
record; there is no redundant processed-ID database. Ordinary sent mail supplies
payload conflict checking and correlation across the acceptance/archival gap.
Repeated scans and read/inbox overlap converge on the same message even if
already POP'd.

The shared recipient lock serializes notification with concurrent agent sends.
Its paste/one-submit, observation and uncertain-replay rules still apply. Failed
notification does not undo accepted mail. Unclaimed accepted archives
participate in recovery; valid claims prevent further input. Missing/corrupt
receipts, changed payloads, unsafe types/paths, conflicting records or read
archives without durable acceptance fail explicitly. Restore evidence rather
than inventing a new request or blindly replaying input. Preserve conflicting
files for reconciliation.

## 5. Interface reports and exact decisions

On genuine notification, POP normally and read the entire returned Markdown
through EOF. Its external guidance identifies the report command:

```sh
tmux-a2a-postman channel-report-heredoc --to m-<claimed-id> --state done <<'BODY'
Describe the subject, actual result, evidence and remaining limitations.
BODY
```

Use `--state blocked` with the concrete blocker when work cannot complete.
`done`/`blocked` are terminal for the exchange; another request uses another
filename. Reports require this interface's POP receipt and active binding. Exact
reruns are idempotent; changed terminal reports are rejected. An acknowledgement
is not task completion. No channel command executes or approves a shell command.

For one human decision, report `--state pending --scope <sha256>` and describe
the complete exact action, revision/context and why a decision is needed. Hash
that complete context. The output presents two exact inbox filenames, also
returned by `channel-sync` catch-up:

```text
answer--<pending-report-message-id>--<scope-sha256>--approve.md
answer--<pending-report-message-id>--<scope-sha256>--decline.md
```

Publish exactly one with the human's reason as plain text and trigger intake. It
becomes another claimed message bound to that decision, original exchange and
scope. Unknown/stale/superseded/changed-scope/conflicting answers are rejected
without routing another action. A new pending report supersedes the old
challenge; a terminal report ends it. Ordinary text such as “proceed” is
conversation only, never authorization. Clarify ambiguous answers with the exact
pending context. A matched choice is local decision evidence, not
execution-review approval.

Report on the claimed answer ID after handling it. The terminal report preserves
the exchange and exact references to both the answer and original request. A
pending report does not close the original terminal obligation.

A terminal report closes all already-claimed mail in that exact exchange,
including superseded answers, without granting them authority over a revised
action. Unread exchange mail must be POP'd and fully read first. The existing
combined-reply bound of 16 claimed messages applies; beyond it the command fails
explicitly instead of claiming the whole exchange is closed.

## 6. Outbox, catch-up and retention

Reports first commit as ordinary outgoing mail, then stage complete
conversational Markdown and atomically rename it to `outbox/<message-id>.md`. A
crash in that gap is recovered by the next scan. Each report includes
subject/body, state, stable exchange/report references and exact decision
instructions when needed.

Catch-up re-exports this binding's retained reports and current pending
decisions. Consumers deduplicate by stable message ID; no external
acknowledgement, deletion or move is required. Local publication proves local
availability, not human or application receipt. Disconnected consumers can
enumerate retained output on reconnect. Errors and enumeration limits are
explicit; do not claim missing history is complete. There is no invented report
cursor or remote principal.

Files remain until user cleanup. Outbox can be reconstructed from retained sent
mail; read is the external consumption record. If history/consumption evidence
is deleted, exactly-once recovery beyond it cannot be promised. Back up the
binding and both histories together. Do not edit descriptors or internal mail by
hand.

## 7. Verification boundary

Go tests exercise retries, changed payloads, interruptions at publication,
acceptance and archival, outbox gaps, restart, exact decisions, supersession,
conflict, foreign bindings and unsafe files. The real-tmux matrix covers done,
blocked and decision round trips with plain and bracketed editors. Only real
submitted hints permit fixture POP/read/respond work; the controller does none
of it. Proofs retain full-file digests and conversation/context nonces.

Optional `--agent codex --case external-decision --mode mock` uses real Codex
TUI and tool execution with a fixed local model server. It establishes same-TUI
transport/tool behavior, not real-model comprehension, remote human identity,
Claude execution or human-draft protection. Paid/live-user trials remain
separate.
