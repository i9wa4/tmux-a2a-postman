package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

// An unread inbox message has not been claimed with pop, so inspect-message must
// not expose its content (#875: pop is the only way to consume mail).
func TestRunInspectMessageRefusesUnpoppedInboxMessage(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010101-from-orchestrator-to-worker.md"
	secretBody := "UNPOPPED-BODY-MARKER please inspect this"
	content := inspectMessageFixture("orchestrator", "worker", filename, map[string]string{
		"replyPolicy":      "required",
		"input_request_id": "ireq_123",
	}, secretBody)
	inboxPath := filepath.Join(fixture.sessionDir, "inbox", "worker", filename)
	writeInspectMessageFile(t, inboxPath, content)

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage() error = %v stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	got := decodeInspectMessageOutputForTest(t, stdout)
	if got.Status != "not_claimed" || got.Message != nil || len(got.Matches) != 0 {
		t.Fatalf("inspect output = %#v, want not_claimed with no message content", got)
	}
	for _, leaked := range []string{secretBody, "ireq_123", inboxPath} {
		if strings.Contains(stdout, leaked) {
			t.Fatalf("not_claimed output leaked %q: %s", leaked, stdout)
		}
	}
	if _, err := os.Stat(inboxPath); err != nil {
		t.Fatalf("inspect-message must not move unread inbox file: %v", err)
	}

	for _, mode := range []string{"--path", "--body"} {
		stdout, _, err := captureCommandOutput(t, func() error {
			return RunInspectMessage([]string{
				"--context-id", fixture.contextID,
				"--session", fixture.sessionName,
				"--id", filename,
				mode,
			})
		})
		if err == nil || !strings.Contains(err.Error(), "not_claimed") {
			t.Fatalf("RunInspectMessage(%s) error = %v, want a not_claimed refusal", mode, err)
		}
		if strings.Contains(stdout, secretBody) || strings.Contains(stdout, inboxPath) {
			t.Fatalf("RunInspectMessage(%s) stdout leaked unpopped content: %q", mode, stdout)
		}
	}
}

// Once the message was popped (archived under read/), the read copy is the only
// match even if an unread copy with the same id is still in the inbox.
func TestRunInspectMessageIgnoresUnreadCopyWhenReadCopyExists(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010108-from-orchestrator-to-worker.md"
	content := inspectMessageFixture("orchestrator", "worker", filename, nil, "popped body")
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	writeLegacyReadArchive(t, readPath, content)
	writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "inbox", "worker", filename), content)

	got := runInspectMessageForFixture(t, fixture, filename)
	if got.Status != "found" || got.MatchCount != 1 || got.Message == nil || got.Message.StorageState != "read" || got.Message.MarkdownPath != readPath {
		t.Fatalf("inspect output = %#v, want exactly the read copy", got)
	}
}

// inspectJournalForFixture opens the fixture session's journal so a test can
// record the delivery and read events the production pop path journals.
func inspectJournalForFixture(t *testing.T, fixture inspectMessageFixtureState) (deliver func(filename, content string), recordRead func(path, messageID, content string)) {
	t.Helper()
	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(fixture.sessionDir, fixture.contextID, fixture.sessionName, 1, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	seq := 0
	deliver = func(filename, content string) {
		t.Helper()
		seq++
		if _, err := writer.AppendEvent(projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
			MessageID: filename,
			From:      "orchestrator",
			To:        "worker",
			Path:      filepath.Join("inbox", "worker", filename),
			Content:   content,
		}, now.Add(time.Duration(seq)*time.Second)); err != nil {
			t.Fatalf("AppendEvent(delivered): %v", err)
		}
	}
	recordRead = func(path, messageID, content string) {
		t.Helper()
		seq++
		if _, err := writer.AppendEvent(projection.MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
			MessageID: messageID,
			From:      "orchestrator",
			To:        "worker",
			Path:      path,
			Content:   content,
		}, now.Add(time.Duration(seq)*time.Second)); err != nil {
			t.Fatalf("AppendEvent(read): %v", err)
		}
	}
	return deliver, recordRead
}

// writeLegacyReadArchive writes a read/ archive and backdates it before the
// journal era, the shape of archives that predate journaled read events.
func writeLegacyReadArchive(t *testing.T, path, content string) {
	t.Helper()
	writeInspectMessageFile(t, path, content)
	old := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes(%s): %v", path, err)
	}
}

// A read/ file alone is not proof of a successful pop: a pop that failed after
// the archive rename can leave an orphan there (#802 rollback race). The proof
// is the same journaled read event that keeps the archive alive through the
// projection sync (#875).
func TestRunInspectMessageClaimedProofFollowsJournaledReads(t *testing.T) {
	secretBody := "ORPHAN-BODY-MARKER"
	assertRefused := func(t *testing.T, fixture inspectMessageFixtureState, filename, wantReason string) {
		t.Helper()
		stdout, stderr, err := captureCommandOutput(t, func() error {
			return RunInspectMessage([]string{"--context-id", fixture.contextID, "--session", fixture.sessionName, "--id", filename})
		})
		if err != nil || stderr != "" {
			t.Fatalf("RunInspectMessage() error = %v stderr=%q", err, stderr)
		}
		got := decodeInspectMessageOutputForTest(t, stdout)
		if got.Status != "not_claimed" || got.Reason != wantReason || got.Message != nil || len(got.Matches) != 0 {
			t.Fatalf("inspect output = %#v, want not_claimed/%s with no message content", got, wantReason)
		}
		if strings.Contains(stdout, secretBody) || strings.Contains(stdout, "read/") {
			t.Fatalf("not_claimed output leaked content or a path: %s", stdout)
		}
		if _, _, err := captureCommandOutput(t, func() error {
			return RunInspectMessage([]string{"--context-id", fixture.contextID, "--session", fixture.sessionName, "--id", filename, "--body"})
		}); err == nil || !strings.Contains(err.Error(), "not_claimed") {
			t.Fatalf("RunInspectMessage(--body) error = %v, want a not_claimed refusal", err)
		}
	}

	t.Run("orphan read with no journaled read", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		deliver, _ := inspectJournalForFixture(t, fixture)
		filename := "20261009-120000-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody)
		deliver(filename, content)
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), content)
		assertRefused(t, fixture, filename, "no_journaled_read")
	})

	t.Run("orphan read plus unread inbox copy", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		deliver, _ := inspectJournalForFixture(t, fixture)
		filename := "20261009-120001-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody)
		deliver(filename, content)
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), content)
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "inbox", "worker", filename), content)
		assertRefused(t, fixture, filename, "no_journaled_read")
	})

	t.Run("journaled read for a different message does not prove the file", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		deliver, recordRead := inspectJournalForFixture(t, fixture)
		orphan := "20261009-120002-from-orchestrator-to-worker.md"
		other := "20261009-120003-from-orchestrator-to-worker.md"
		orphanContent := inspectMessageFixture("orchestrator", "worker", orphan, nil, secretBody)
		otherContent := inspectMessageFixture("orchestrator", "worker", other, nil, "another popped message")
		deliver(orphan, orphanContent)
		deliver(other, otherContent)
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", orphan), orphanContent)
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", other), otherContent)
		recordRead(filepath.Join("read", other), other, otherContent)
		assertRefused(t, fixture, orphan, "no_journaled_read")
	})

	t.Run("journaled read at the path but for another message id", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		deliver, recordRead := inspectJournalForFixture(t, fixture)
		filename := "20261009-120004-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody)
		deliver(filename, content)
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), content)
		// The journal's read at this path carries a different message's content.
		recordRead(filepath.Join("read", filename), "20261009-999999-from-someone-else.md", inspectMessageFixture("orchestrator", "worker", "20261009-999999-from-someone-else.md", nil, "someone else's body"))
		assertRefused(t, fixture, filename, "no_journaled_read")
	})

	t.Run("journaled read is found and agrees with the projection", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		deliver, recordRead := inspectJournalForFixture(t, fixture)
		filename := "20261009-120005-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, "popped body")
		deliver(filename, content)
		readPath := filepath.Join(fixture.sessionDir, "read", filename)
		writeInspectMessageFile(t, readPath, content)
		recordRead(filepath.Join("read", filename), filename, content)
		got := runInspectMessageForFixture(t, fixture, filename)
		if got.Status != "found" || got.MatchCount != 1 || got.Message == nil || got.Message.MarkdownPath != readPath {
			t.Fatalf("inspect output = %#v, want the journaled read archive", got)
		}
		projected, ok, err := projection.ProjectMailboxProjection(fixture.sessionDir)
		if err != nil || !ok {
			t.Fatalf("ProjectMailboxProjection() = (_, %v, %v)", ok, err)
		}
		inProjection := false
		for _, file := range projected.Read {
			if filepath.ToSlash(file.Path) == "read/"+filename {
				inProjection = true
			}
		}
		if !inProjection {
			t.Fatalf("claimed message is not in the projection read set: %#v", projected.Read)
		}
	})

	// G875-2: an old-named message that was delivered and then orphaned now has a
	// recent file time, so the date fallback must not accept it while the journal
	// has no read for it.
	t.Run("old-named orphan is not legacy", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		deliver, _ := inspectJournalForFixture(t, fixture)
		filename := "20260501-120000-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody)
		deliver(filename, content)
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), content)
		assertRefused(t, fixture, filename, "no_journaled_read")
	})

	// F-002: a rename preserves the file time, so an orphan delivered long ago
	// (or a message popped and orphaned later) can keep a pre-cutoff mtime. The
	// journal shows it as delivered with no Read, so the legacy fallback must
	// not accept it.
	t.Run("backdated orphan the journal shows as delivered without a read is not legacy", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		deliver, _ := inspectJournalForFixture(t, fixture)
		filename := "20260501-120003-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody)
		deliver(filename, content)
		writeLegacyReadArchive(t, filepath.Join(fixture.sessionDir, "read", filename), content)
		assertRefused(t, fixture, filename, "no_journaled_read")
	})

	// F-001: a journaled first read with EMPTY content is a tombstone in the
	// projection (the sync keeps the archive but the projection has no read
	// entry). The chosen behavior is to fail closed: after a real pop and a sync,
	// inspect-message reports not_claimed. This test pins that documented false
	// negative so a change to it is a deliberate one.
	t.Run("empty-content journaled read after a real pop stays not claimed", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		deliver, recordRead := inspectJournalForFixture(t, fixture)
		filename := "20261009-130100-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody)
		inboxDir := filepath.Join(fixture.sessionDir, "inbox", "worker")
		writeInspectMessageFile(t, filepath.Join(inboxDir, filename), content)
		deliver(filename, content)

		var popOut bytes.Buffer
		if err := runPopWithContext(commandContext{
			stdout:           &popOut,
			resolveInboxPath: func(args []string) (string, error) { return inboxDir, nil },
			loadConfig:       func(path string) (*config.Config, error) { return config.DefaultConfig(), nil },
			contextOwnsSession: func(baseDir, resolvedContextID, name string) bool {
				return false
			},
		}, []string{"--context-id", fixture.contextID}); err != nil {
			t.Fatalf("runPopWithContext: %v", err)
		}
		readPath := filepath.Join(fixture.sessionDir, "read", filename)
		if _, err := os.Stat(readPath); err != nil {
			t.Fatalf("archived file missing after pop: %v", err)
		}
		recordRead(filepath.Join("read", filename), filename, "")
		if err := projection.SyncMailboxProjection(fixture.sessionDir); err != nil {
			t.Fatalf("SyncMailboxProjection: %v", err)
		}
		if _, err := os.Stat(readPath); err != nil {
			t.Fatalf("sync removed the archive of an empty-content read: %v", err)
		}
		assertRefused(t, fixture, filename, "no_journaled_read")
	})

	// F-001 (round 2): pop's rename keeps the INBOX file's time on the archive, so
	// a real pop of a message whose inbox file has an old time yields an archive
	// with a pre-cutoff mtime. With a tombstoned (empty-content) journaled read the
	// legacy fallback must not accept it either: not_claimed, archive retained.
	t.Run("old-mtime real pop with an empty-content journaled read stays not claimed", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		deliver, recordRead := inspectJournalForFixture(t, fixture)
		filename := "20260501-120004-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody)
		inboxDir := filepath.Join(fixture.sessionDir, "inbox", "worker")
		inboxPath := filepath.Join(inboxDir, filename)
		writeLegacyReadArchive(t, inboxPath, content) // old file time on the INBOX file
		// Delivered, then consumed by the read event below; the old file time
		// survives pop's rename.
		deliver(filename, content)

		var popOut bytes.Buffer
		if err := runPopWithContext(commandContext{
			stdout:           &popOut,
			resolveInboxPath: func(args []string) (string, error) { return inboxDir, nil },
			loadConfig:       func(path string) (*config.Config, error) { return config.DefaultConfig(), nil },
			contextOwnsSession: func(baseDir, resolvedContextID, name string) bool {
				return false
			},
		}, []string{"--context-id", fixture.contextID}); err != nil {
			t.Fatalf("runPopWithContext: %v", err)
		}
		readPath := filepath.Join(fixture.sessionDir, "read", filename)
		info, err := os.Stat(readPath)
		if err != nil {
			t.Fatalf("archived file missing after pop: %v", err)
		}
		if !info.ModTime().Before(time.Date(2026, time.June, 30, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("archive mtime = %v, want the old inbox file time preserved by pop's rename", info.ModTime())
		}
		recordRead(filepath.Join("read", filename), filename, "")
		if err := projection.SyncMailboxProjection(fixture.sessionDir); err != nil {
			t.Fatalf("SyncMailboxProjection: %v", err)
		}
		if _, err := os.Stat(readPath); err != nil {
			t.Fatalf("sync removed the archive of an empty-content read: %v", err)
		}
		assertRefused(t, fixture, filename, "no_journaled_read")
	})

	t.Run("backdated archive without journal evidence is legacy", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		_, _ = inspectJournalForFixture(t, fixture)
		filename := "20260501-120001-from-orchestrator-to-worker.md"
		readPath := filepath.Join(fixture.sessionDir, "read", filename)
		writeLegacyReadArchive(t, readPath, inspectMessageFixture("orchestrator", "worker", filename, nil, "legacy body"))
		got := runInspectMessageForFixture(t, fixture, filename)
		if got.Status != "found" || got.Message == nil || got.Message.MarkdownPath != readPath {
			t.Fatalf("inspect output = %#v, want the legacy read archive found", got)
		}
	})

	t.Run("journal decides when it has a read for the path, even for a backdated file", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		_, recordRead := inspectJournalForFixture(t, fixture)
		filename := "20260501-120002-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody)
		writeLegacyReadArchive(t, filepath.Join(fixture.sessionDir, "read", filename), content)
		recordRead(filepath.Join("read", filename), "20261009-999999-from-someone-else.md", inspectMessageFixture("orchestrator", "worker", "20261009-999999-from-someone-else.md", nil, "someone else's body"))
		assertRefused(t, fixture, filename, "no_journaled_read")
	})

	t.Run("recent archive in a session with no journal is not claimed", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		filename := "20261009-120006-from-orchestrator-to-worker.md"
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody))
		assertRefused(t, fixture, filename, "no_journaled_read")
	})
}

// O4 (#875): the real pop path, the real projection sync, then inspect-message.
// This is the sequence that broke the pop-receipt proof: sync deletes files
// under read/ that no journaled event accounts for, including receipts.
func TestRunInspectMessageFindsRealPopAfterProjectionSync(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	deliver, recordRead := inspectJournalForFixture(t, fixture)
	filename := "20261009-130000-from-orchestrator-to-worker.md"
	body := "REAL-POP-BODY-MARKER"
	content := inspectMessageFixture("orchestrator", "worker", filename, nil, body)
	inboxDir := filepath.Join(fixture.sessionDir, "inbox", "worker")
	writeInspectMessageFile(t, filepath.Join(inboxDir, filename), content)
	deliver(filename, content)

	var popOut bytes.Buffer
	if err := runPopWithContext(commandContext{
		stdout:           &popOut,
		resolveInboxPath: func(args []string) (string, error) { return inboxDir, nil },
		loadConfig:       func(path string) (*config.Config, error) { return config.DefaultConfig(), nil },
		contextOwnsSession: func(baseDir, resolvedContextID, name string) bool {
			return false
		},
	}, []string{"--context-id", fixture.contextID}); err != nil {
		t.Fatalf("runPopWithContext: %v", err)
	}
	if !strings.Contains(popOut.String(), filename) {
		t.Fatalf("pop output does not name the message: %s", popOut.String())
	}
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	archived, err := os.ReadFile(readPath)
	if err != nil {
		t.Fatalf("archived file missing after pop: %v", err)
	}
	// The daemon or its read watcher journals the read event with content.
	recordRead(filepath.Join("read", filename), filename, string(archived))
	if err := projection.SyncMailboxProjection(fixture.sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection: %v", err)
	}

	got := runInspectMessageForFixture(t, fixture, filename)
	if got.Status != "found" || got.MatchCount != 1 || got.Message == nil || got.Message.MarkdownPath != readPath {
		t.Fatalf("inspect output after pop and sync = %#v, want the popped message found", got)
	}
}

func TestRunInspectMessageFindsReadMessageAfterInputRequestSatisfied(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010102-from-worker-to-orchestrator.md"
	content := inspectMessageFixture("worker", "orchestrator", filename, map[string]string{
		"replyPolicy":            "none",
		"replyTo":                "20260506-010101-from-orchestrator-to-worker.md",
		"fills_input_request_id": "ireq_123",
	}, "DONE: handled")
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	writeLegacyReadArchive(t, readPath, content)

	got := runInspectMessageForFixture(t, fixture, filename)
	if got.Status != "found" || got.MatchCount != 1 || got.Message == nil {
		t.Fatalf("inspect output = %#v, want one found read message", got)
	}
	if got.Message.StorageState != "read" {
		t.Fatalf("storage_state = %q, want read", got.Message.StorageState)
	}
	if got.Message.MarkdownPath != readPath {
		t.Fatalf("markdown_path = %q, want %q", got.Message.MarkdownPath, readPath)
	}
	if got.Message.FillsInputRequestID != "ireq_123" || got.Message.ReplyTo != "20260506-010101-from-orchestrator-to-worker.md" {
		t.Fatalf("reply metadata = %#v, want satisfied input request metadata", got.Message)
	}
}

func TestRunInspectMessageFindsStoredMessageAfterInspectInputIsClosed(t *testing.T) {
	fixture := writeInspectInputFixture(t)
	messageID := "20260506-010105-from-critic-to-worker.md"
	inputRequestID := "ireq_closed"
	appendInspectInputRequest(t, fixture, messageID, inputRequestID)
	appendInspectInputResolution(t, fixture, "20260506-010106-from-worker-to-critic.md", messageID, inputRequestID)

	inspectInput := runInspectInputForFixture(t, fixture, messageID)
	if inspectInput.Status != "not_found" || inspectInput.MatchCount != 0 {
		t.Fatalf("inspect-input output = %#v, want closed input request not_found", inspectInput)
	}

	content := sessionStatusMessageContent("critic", "worker", messageID, map[string]string{
		"replyPolicy":      "required",
		"input_request_id": inputRequestID,
	}, "Original request body")
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	readPath := filepath.Join(sessionDir, "read", messageID)
	// The fixture journaled a delivery for this message, so a real pop would
	// also have journaled the read that consumes it (F-002 refuses a delivered
	// message with no matching read, however old the file is).
	writeInspectMessageFile(t, readPath, content)
	readAt := time.Date(2026, time.April, 14, 6, 10, 0, 0, time.UTC)
	readWriter, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 612, readAt)
	if err != nil {
		t.Fatalf("OpenShadowWriter(read) error = %v", err)
	}
	if _, err := readWriter.AppendEvent(projection.MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID: messageID,
		From:      "critic",
		To:        "worker",
		Path:      filepath.Join("read", messageID),
		Content:   content,
	}, readAt); err != nil {
		t.Fatalf("AppendEvent(read): %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--config", fixture.configPath,
			"--id", messageID,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage() error = %v stderr=%q", err, stderr)
	}
	got := decodeInspectMessageOutputForTest(t, stdout)
	if got.Status != "found" || got.MatchCount != 1 || got.Message == nil {
		t.Fatalf("inspect-message output = %#v, want persisted read message", got)
	}
	if got.Message.MarkdownPath != readPath || got.Message.InputRequestID != inputRequestID {
		t.Fatalf("stored message = %#v, want read path and original input metadata", got.Message)
	}
}

func TestRunInspectMessageOutputModes(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010103-from-orchestrator-to-worker.md"
	content := inspectMessageFixture("orchestrator", "worker", filename, nil, "Body line one\n\nBody line two")
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	writeLegacyReadArchive(t, readPath, content)

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
			"--path",
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage(--path) error = %v stderr=%q", err, stderr)
	}
	if stdout != readPath+"\n" {
		t.Fatalf("--path stdout = %q, want %q", stdout, readPath+"\n")
	}

	stdout, stderr, err = captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage(--json) error = %v stderr=%q", err, stderr)
	}
	got := decodeInspectMessageOutputForTest(t, stdout)
	if got.Status != "found" || got.Message == nil || got.Message.MarkdownPath != readPath {
		t.Fatalf("--json output = %#v, want structured found message", got)
	}

	stdout, stderr, err = captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
			"--body",
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage(--body) error = %v stderr=%q", err, stderr)
	}
	if stdout != "Body line one\n\nBody line two\n" {
		t.Fatalf("--body stdout = %q, want body only", stdout)
	}
}

func TestRunInspectMessageBodyReturnsSenderBodyAfterEnvelopeSeparator(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010106-from-orchestrator-to-worker.md"
	senderBody := "# User Request\n\n---\n\n## Details\n\n```sh\n# keep literal\n```\n"
	content := strings.Join([]string{
		"---",
		"params:",
		"  from: orchestrator",
		"  to: worker",
		"  messageId: " + filename,
		"  timestamp: 2026-05-06T01:01:06Z",
		"---",
		"",
		"# Message",
		"",
		"## Recipient Instructions",
		"",
		"Generated guidance before body.",
		"",
		"## Sender Message",
		"",
		envelope.SenderBodyBoundaryForMessageID(filename),
		"---",
		"",
	}, "\n") + senderBody
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	writeLegacyReadArchive(t, readPath, content)

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
			"--body",
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage(--body) error = %v stderr=%q", err, stderr)
	}
	if stdout != senderBody {
		t.Fatalf("--body stdout changed sender body:\n got %q\nwant %q", stdout, senderBody)
	}
}

func TestRunInspectMessageBodyKeepsOrdinaryMarkdownHorizontalRule(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010107-from-orchestrator-to-worker.md"
	body := "Intro\n\n---\n\nDetails"
	content := inspectMessageFixture("orchestrator", "worker", filename, nil, body)
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	writeLegacyReadArchive(t, readPath, content)

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
			"--body",
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage(--body) error = %v stderr=%q", err, stderr)
	}
	if stdout != body+"\n" {
		t.Fatalf("--body stdout changed ordinary body:\n got %q\nwant %q", stdout, body+"\n")
	}
}

func TestRunInspectMessageReturnsNotFoundAndAmbiguous(t *testing.T) {
	t.Run("wrong id", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		got := runInspectMessageForFixture(t, fixture, "missing.md")
		if got.Status != "not_found" || got.MatchCount != 0 || got.Message != nil {
			t.Fatalf("inspect output = %#v, want not_found", got)
		}
	})

	t.Run("ambiguous id", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		filename := "20260506-010104-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, "duplicate")
		// Two archived files that carry the same messageId in their metadata.
		writeLegacyReadArchive(t, filepath.Join(fixture.sessionDir, "read", filename), content)
		writeLegacyReadArchive(t, filepath.Join(fixture.sessionDir, "read", "20260504-000000-copy-of-"+filename), content)

		got := runInspectMessageForFixture(t, fixture, filename)
		if got.Status != "ambiguous" || got.MatchCount != 2 || got.Message != nil {
			t.Fatalf("inspect output = %#v, want ambiguous two matches", got)
		}
		if len(got.Matches) != 2 {
			t.Fatalf("matches len = %d, want 2", len(got.Matches))
		}
	})
}

type inspectMessageFixtureState struct {
	baseDir     string
	contextID   string
	sessionName string
	sessionDir  string
}

func writeInspectMessageFixture(t *testing.T) inspectMessageFixtureState {
	t.Helper()
	baseDir := t.TempDir()
	contextID := "ctx-inspect-message"
	sessionName := "test-session"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	installFakeTmuxForCLI(t, baseDir, sessionName, "worker")
	if err := os.MkdirAll(filepath.Join(sessionDir, "inbox", "worker"), 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sessionDir, "read"), 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}
	return inspectMessageFixtureState{
		baseDir:     baseDir,
		contextID:   contextID,
		sessionName: sessionName,
		sessionDir:  sessionDir,
	}
}

func inspectMessageFixture(from, to, messageID string, fields map[string]string, body string) string {
	var builder strings.Builder
	builder.WriteString("---\nparams:\n")
	builder.WriteString("  from: " + from + "\n")
	builder.WriteString("  to: " + to + "\n")
	builder.WriteString("  messageId: " + messageID + "\n")
	builder.WriteString("  timestamp: 2026-05-06T01:01:01Z\n")
	for _, key := range []string{"replyPolicy", "replyTo", "input_request_id", "fills_input_request_id", "input_request_set_id", "branch_id", "completion_rule"} {
		if value := fields[key]; value != "" {
			builder.WriteString("  " + key + ": " + value + "\n")
		}
	}
	builder.WriteString("---\n\n")
	builder.WriteString(body)
	builder.WriteString("\n")
	return builder.String()
}

func writeInspectMessageFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func runInspectMessageForFixture(t *testing.T, fixture inspectMessageFixtureState, id string) inspectMessageOutput {
	t.Helper()
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", id,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage() error = %v stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	return decodeInspectMessageOutputForTest(t, stdout)
}

func decodeInspectMessageOutputForTest(t *testing.T, stdout string) inspectMessageOutput {
	t.Helper()
	var got inspectMessageOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	return got
}
