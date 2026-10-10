package projection

import "github.com/i9wa4/tmux-a2a-postman/internal/journal"

// CountPopVerificationFailures returns how many
// MailboxProjectionPopVerificationFailedEventType events have been recorded
// for filename in this session's journal (#755 F-013). handleDaemonSubmitPop
// calls this after appending a fresh failure event, to decide whether a
// verification failure should still roll the message back to inbox for
// another attempt, or has failed persistently enough (N=3) to give up and
// dead-letter it instead.
//
// Keyed by filename alone, not filename+node: archived messages already
// land in a single, flat session-level read/ directory
// (store.PlanArchiveInboxMessage joins sessionDir/read/<filename>, with no
// per-node segment), and the existing dedup-archive branch in
// archiveInboxMessageWithOps already treats filename as the effective
// identity key within a session for exactly that reason. Filename
// uniqueness is not independently enforced anywhere --
// message.GenerateFilename only guards against collision with a 16-bit
// random nonce -- but keying this counter more narrowly (e.g. by node) than
// that pre-existing invariant would be inconsistent with it, not safer: the
// dedup branch would already conflate two same-named messages from
// different nodes before this counter ever saw them.
//
// Uses journal.ReplayEach (a streaming callback) rather than journal.Replay
// (which retains every event in memory) since a session journal grows
// without bound over the session's lifetime and this only needs to count
// matches, never retain the events themselves -- all-or-nothing replay
// semantics with unbounded memory retention is the wrong tradeoff here
// regardless of how large any journal has been observed to get in practice.
func CountPopVerificationFailures(sessionDir, filename string) (int, error) {
	count := 0
	err := journal.ReplayEach(sessionDir, func(event journal.Event) error {
		if event.Type != MailboxProjectionPopVerificationFailedEventType {
			return nil
		}
		payload, ok := decodeMailboxEventPayload(event.Payload)
		if !ok {
			return nil
		}
		if payload.MessageID == filename {
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
