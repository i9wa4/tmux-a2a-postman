# Issue #1111: local time in human displays

Status: display policy for the separately authorized #1111 implementation.
The runtime and Help docs implement this policy; canonical IDs and stored fields
remain UTC.

## 1. Boundary and current surfaces

An instant has one canonical representation for identity, persistence, ordering
and machine parsing. Local time is a presentation of that instant, with a
numeric UTC offset. A displayed date must never replace an ID, archive path,
receipt, comparison key or stored timestamp.

| Surface                                     | Current owner and behavior                                                                                                                                                                                                                                                                | Policy                                                                                                                                                                                                                                                                                                                       |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Mail IDs and `*.md` basenames               | `internal/messageid/id.go` encodes a fixed-width UTC timestamp; `internal/workspace/message_identity.go` creates it. `internal/workspace/mail.go` checks it against `created_at`.                                                                                                         | Keep exact UTC ID and filename, including legacy IDs. Never rename or parse a local wall time into an ID.                                                                                                                                                                                                                    |
| Archive metadata and claim receipt          | `internal/workspace/mail.go` stores `created_at` and `claimed_at`; `internal/workspace/message_context.go` writes immutable archive metadata.                                                                                                                                             | Keep UTC instants and exact reply targets. The timestamp in a legacy random-only ID is unavailable; use verified `created_at`.                                                                                                                                                                                               |
| Recipient Markdown                          | `internal/cli/recipient_context.go` already converts `created_at` with `time.Local` for `Timestamp` and the configured `{timestamp}` footer. The metadata comment remains canonical.                                                                                                      | Keep this visible format, but use the shared Go formatter and pass the location into rendering. Preserve existing archives byte-for-byte.                                                                                                                                                                                    |
| POP path and CLI operation JSON             | `internal/cli/mail_commands.go` returns a canonical `markdown_absolute_path`, a `cat` instruction, and UTC `as_of`. Other operational results in `internal/cli/execution_commands.go`, `internal/cli/external_commands.go` and `internal/workspace/execution.go` also expose UTC `as_of`. | Retain those fields and values. Add explicitly named local display siblings to newly emitted results: `display_created_at` for a successful claimed POP, derived from the claimed message's validated `created_at`, and `display_as_of` where `as_of` is emitted for an operator. Keep the exact path and command unchanged. |
| Saved status and one-line view              | `internal/workspace/status_refresh.go` stores UTC `started_at`, `as_of` and capture times; `internal/cli/status_commands.go` returns saved JSON bytes unchanged. `get-status-oneline` shows glyphs, not timestamp text.                                                                   | Do not rewrite saved status or add time text to the compact glyph view. Any future human status detail should format the saved instant at display time.                                                                                                                                                                      |
| Invocation diagnostics                      | `internal/diagnostic/recorder.go` uses UTC for the JSONL `time` field and the `YYYY-MM-DD` log directory; `help/invocation-diagnostics.md` defines both.                                                                                                                                  | Preserve the UTC schema field and directory. Add a local `display_time` sibling to each new event using the same formatter; do not change duration or log correlation semantics.                                                                                                                                             |
| Historical selection and external filenames | `skills/postman-records/scripts/select_day.py` selects UTC days from verified `created_at`; `internal/workspace/external.go` embeds stable IDs in report and decision filenames.                                                                                                          | Keep UTC selection and exact filenames. Local dates are labels, never history boundaries or decision targets.                                                                                                                                                                                                                |

`internal/workspace/notification.go` sends a generic POP hint. It does not
identify which message POP will claim, so it must not show a particular mail
timestamp. A POP's local display time belongs to the actual claim result.

## 2. Shared Go display rule

Use one small internal formatter, owned by Go, for all newly rendered local
timestamps. Its input is a validated `time.Time` instant and an explicit
`*time.Location`; the caller selects the machine's `time.Local` once at the
presentation boundary. Format as
`2006-01-02T15:04:05.999999999-07:00`. The numeric offset is mandatory,
including `+00:00` in UTC. Fractional seconds follow Go's existing trimmed
`RFC3339Nano` behavior. A zone abbreviation may accompany the numeric offset
in prose, but must not replace it or be required for parsing.

Callers that start with stored text parse it as `time.RFC3339Nano` and handle an
invalid value as an error or unknown result; they must not present malformed
text as if it were a valid local time. The recipient helper reports invalid
input explicitly instead of displaying malformed stored text as a plausible
time. Avoid a new timezone setting, global `TZ` mutation, per-command layouts,
or extra state. Keep IDs, persisted records and machine fields on their existing
UTC writers; only presentation calls the formatter.

For example, a POP result may contain the unchanged
`markdown_absolute_path` ending in
`m-20261101T163000.000000000Z-Ab12Cd.md` and a sibling
`display_created_at: "2026-11-02T01:30:00+09:00"` on a machine in Tokyo.
The display value describes the claimed message, not filesystem mtime or the
time POP ran. `as_of` and `display_as_of` describe the operation's one sampled
instant; do not call the clock twice to produce the pair.

## 3. Deterministic verification

- Test the formatter with an injected location and fixed UTC instants. Use
  `America/New_York` with embedded Go `time/tzdata`, as the existing recipient
  tests do, so the test does not depend on the host timezone database.
- Check the autumn repeated hour: `2026-11-01T05:30:00Z` displays
  `2026-11-01T01:30:00-04:00`, while `2026-11-01T06:30:00Z` displays
  `2026-11-01T01:30:00-05:00`. The wall clock matches; the offsets distinguish
  the instants. Also check the spring gap, a date rollover, nanoseconds and
  UTC `+00:00`.
- Inject the location into recipient, POP/result and diagnostic rendering
  tests. Assert that canonical ID, path, `created_at`, `claimed_at`, `as_of`,
  diagnostic `time` and UTC log directory are unchanged. Avoid assigning
  `time.Local` in tests; one existing recipient test currently does so.
- Cover legacy random-only IDs by deriving display time from validated archive
  metadata, and malformed timestamp input by reporting unknown/error rather
  than a plausible local value. Test that an empty POP has no message display
  time and that its generic notification remains unbound to an ID.
- Check output compatibility: existing JSON keys keep their meaning, exact
  `get-status` bytes remain intact, diagnostic event size/byte limits still
  hold, and the recipient's configured `{timestamp}` uses the shared rule.
