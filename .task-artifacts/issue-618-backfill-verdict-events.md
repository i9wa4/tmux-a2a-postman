# Issue 618 Backfill Verdict Events

## 1. Task

Implement the D1c archive backfill lane for issue #618: walk read archives,
extract terminal verdict markers for all node pairs, and emit idempotent
`verdict_event` rows tagged as backfill provenance.

## 2. Original Checklist

- [x] Script walks existing message archives and extracts from-node, to-node,
  timestamp, and terminal marker (`PASS`/`BLOCKED`/`NOT APPROVED`/`DONE`).
- [x] Generalizes the existing single-node-pair approval parser behavior to all
  node pairs in the archive.
- [x] Produces `verdict_event`-shaped rows tagged with `source: backfill`.
- [x] Backfilled rows leave evidence and identity-tuple fields explicitly
  empty/unknown for pre-D1a history.
- [x] Backfill is idempotent and re-runnable without duplicating events.

## 3. Implementation Notes

- Added `internal/verdictbackfill`, which parses read archive Markdown using
  envelope metadata and first-line terminal markers.
- Added `tmux-a2a-postman backfill-verdict-events` as a standalone JSONL
  command with `--session-dir` and `--archive-dir`.
- Deterministic `event_id` values are derived from source, archive identity,
  node pair, timestamp, and marker so repeated runs emit stable rows.
- The command does not mutate journal state; downstream import can upsert or
  dedupe on `event_id`.

## 4. Changed Files

- `internal/verdictbackfill/backfill.go`
- `internal/verdictbackfill/backfill_test.go`
- `internal/cli/backfill_verdict_events.go`
- `internal/cli/backfill_verdict_events_test.go`
- `internal/cli/dispatch.go`
- `internal/cli/dispatch_test.go`
- `internal/cli/help.go`
- `internal/cli/helptext/backfill-verdict-events.txt`
- `internal/cli/helptext/commands.txt`
- `internal/cli/helptext/overview.txt`
- `main.go`
- `.task-artifacts/issue-618-backfill-verdict-events.md`

## 5. Evidence Log

- PASS: `go test ./internal/verdictbackfill -count=1`
- PASS:

  ```sh
  go test ./internal/cli \
    -run 'TestRunBackfillVerdictEvents|TestDispatch_BackfillVerdictEvents|TestRunHelp' \
    -count=1
  ```

- PASS: `go test ./... -count=1`
- PASS: manual CLI smoke test emitted one `verdict_event` JSONL row with
  `source:"backfill"`, marker `DONE`, empty evidence, and unknown identity
  fields.
- PASS: README and `skills/*/SKILL.md` deprecated-reference scan found no stale
  command, flag, or package references.
- PASS: `git diff --check`
- PASS: `nix flake check`
- PASS: `nix build`

## 6. Remaining Blockers

- None currently.
