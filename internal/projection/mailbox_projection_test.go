package projection

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestProjectMailboxProjection_FourDirectoryLifecycle(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 3, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	appendMailboxEventForTest(t, writer, MailboxProjectionPostedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "20260414-030001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("post", "20260414-030001-r1111-from-orchestrator-to-worker.md"),
		Content:   "queued body",
	}, now.Add(1*time.Second))
	appendMailboxEventForTest(t, writer, MailboxProjectionPostedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "20260414-030002-r2222-from-orchestrator-to-critic.md",
		From:      "orchestrator",
		To:        "critic",
		Path:      filepath.Join("post", "20260414-030002-r2222-from-orchestrator-to-critic.md"),
		Content:   "dead-letter body",
	}, now.Add(2*time.Second))
	appendMailboxEventForTest(t, writer, MailboxProjectionPostConsumedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "20260414-030001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("post", "20260414-030001-r1111-from-orchestrator-to-worker.md"),
	}, now.Add(3*time.Second))
	appendMailboxEventForTest(t, writer, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "20260414-030001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("inbox", "worker", "20260414-030001-r1111-from-orchestrator-to-worker.md"),
		Content:   "queued body",
	}, now.Add(4*time.Second))
	appendMailboxEventForTest(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID: "20260414-030001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("read", "20260414-030001-r1111-from-orchestrator-to-worker.md"),
		Content:   "queued body",
	}, now.Add(5*time.Second))
	appendMailboxEventForTest(t, writer, MailboxProjectionDeadLetteredEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID:  "20260414-030002-r2222-from-orchestrator-to-critic-dl-routing-denied.md",
		From:       "orchestrator",
		To:         "critic",
		Path:       filepath.Join("dead-letter", "20260414-030002-r2222-from-orchestrator-to-critic-dl-routing-denied.md"),
		SourcePath: filepath.Join("post", "20260414-030002-r2222-from-orchestrator-to-critic.md"),
		Content:    "dead-letter body",
	}, now.Add(6*time.Second))

	projected, ok, err := ProjectMailboxProjection(sessionDir)
	if err != nil {
		t.Fatalf("ProjectMailboxProjection() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxProjection() ok = false, want true")
	}

	if got := projected.Post[pathKey(filepath.Join("post", "20260414-030001-r1111-from-orchestrator-to-worker.md"))]; got.Content != "" {
		t.Fatalf("post projection still contains delivered message: %#v", got)
	}
	if got := projected.Post[pathKey(filepath.Join("post", "20260414-030002-r2222-from-orchestrator-to-critic.md"))]; got.Content != "" {
		t.Fatalf("post projection still contains dead-lettered message: %#v", got)
	}
	if got := projected.Inbox[pathKey(filepath.Join("inbox", "worker", "20260414-030001-r1111-from-orchestrator-to-worker.md"))]; got.Content != "" {
		t.Fatalf("inbox projection still contains archived message: %#v", got)
	}
	if got := projected.Read[pathKey(filepath.Join("read", "20260414-030001-r1111-from-orchestrator-to-worker.md"))]; got.Content != "queued body" {
		t.Fatalf("read projection content = %q, want queued body", got.Content)
	}
	if got := projected.DeadLetter[pathKey(filepath.Join("dead-letter", "20260414-030002-r2222-from-orchestrator-to-critic-dl-routing-denied.md"))]; got.Content != "dead-letter body" {
		t.Fatalf("dead-letter projection content = %q, want dead-letter body", got.Content)
	}
}

func TestProjectMailboxProjection_ControlPlaneOnlyExcluded(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 4, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	appendMailboxEventForTest(t, writer, MailboxProjectionPostedEventType, journal.VisibilityControlPlaneOnly, journal.MailboxEventPayload{
		MessageID: "20260414-040001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("post", "20260414-040001-r1111-from-orchestrator-to-worker.md"),
		Content:   "hidden body",
	}, now.Add(time.Second))

	projected, ok, err := ProjectMailboxProjection(sessionDir)
	if err != nil {
		t.Fatalf("ProjectMailboxProjection() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxProjection() ok = false, want true")
	}
	if len(projected.Post) != 0 || len(projected.Inbox) != 0 || len(projected.Read) != 0 || len(projected.DeadLetter) != 0 {
		t.Fatalf("control-plane event leaked into mailbox projection: %#v", projected)
	}
}

func TestSyncMailboxProjection_GenerationQuarantine(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 5, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	appendMailboxEventForTest(t, writer, MailboxProjectionPostedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "20260414-050001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("post", "20260414-050001-r1111-from-orchestrator-to-worker.md"),
		Content:   "queued body",
	}, now.Add(time.Second))

	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection() error = %v", err)
	}

	if _, _, err := journal.ResolveSession(sessionDir, "review", journal.ResolutionExplicitRebind, now.Add(2*time.Second)); err != nil {
		t.Fatalf("ResolveSession(explicit rebind) error = %v", err)
	}
	if _, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 102, now.Add(3*time.Second)); err != nil {
		t.Fatalf("OpenShadowWriter(rebind) error = %v", err)
	}

	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection(rebind) error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(sessionDir, "snapshot", "quarantine", "*", "post", "20260414-050001-r1111-from-orchestrator-to-worker.md"))
	if err != nil {
		t.Fatalf("Glob(quarantine): %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("quarantine matches = %d, want 1", len(matches))
	}
}

func TestSyncMailboxProjection_PreservesUnprojectedPostFiles(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 5, 30, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	appendMailboxEventForTest(t, writer, MailboxProjectionPostedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "20260414-053001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("post", "20260414-053001-r1111-from-orchestrator-to-worker.md"),
		Content:   "projected body",
	}, now.Add(time.Second))

	unprojectedPath := filepath.Join(sessionDir, "post", "20260414-053002-r2222-from-orchestrator-to-worker.md")
	if err := os.MkdirAll(filepath.Dir(unprojectedPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(post): %v", err)
	}
	if err := os.WriteFile(unprojectedPath, []byte("live pending body"), 0o600); err != nil {
		t.Fatalf("WriteFile(unprojected post): %v", err)
	}

	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection() error = %v", err)
	}
	got, err := os.ReadFile(unprojectedPath)
	if err != nil {
		t.Fatalf("unprojected post file was removed: %v", err)
	}
	if string(got) != "live pending body" {
		t.Fatalf("unprojected post content = %q, want live pending body", string(got))
	}
}

func TestSyncMailboxProjection_RemovesConsumedProjectedPostFiles(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 5, 45, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	projectedName := "20260414-054501-r1111-from-orchestrator-to-worker.md"
	projectedPath := filepath.Join(sessionDir, "post", projectedName)
	projectedRel := filepath.Join("post", projectedName)
	appendMailboxEventForTest(t, writer, MailboxProjectionPostedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: projectedName,
		From:      "orchestrator",
		To:        "worker",
		Path:      projectedRel,
		Content:   "projected body",
	}, now.Add(time.Second))

	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection(initial) error = %v", err)
	}
	if got, err := os.ReadFile(projectedPath); err != nil || string(got) != "projected body" {
		t.Fatalf("projected post after initial sync = %q, %v; want projected body", string(got), err)
	}

	unprojectedPath := filepath.Join(sessionDir, "post", "20260414-054502-r2222-from-orchestrator-to-worker.md")
	if err := os.WriteFile(unprojectedPath, []byte("live pending body"), 0o600); err != nil {
		t.Fatalf("WriteFile(unprojected post): %v", err)
	}
	appendMailboxEventForTest(t, writer, MailboxProjectionPostConsumedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: projectedName,
		From:      "orchestrator",
		To:        "worker",
		Path:      projectedRel,
	}, now.Add(2*time.Second))
	appendMailboxEventForTest(t, writer, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: projectedName,
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("inbox", "worker", projectedName),
		Content:   "projected body",
	}, now.Add(3*time.Second))

	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection(consumed) error = %v", err)
	}
	if _, err := os.Stat(projectedPath); !os.IsNotExist(err) {
		t.Fatalf("consumed projected post still exists or wrong error: %v", err)
	}
	if got, err := os.ReadFile(unprojectedPath); err != nil || string(got) != "live pending body" {
		t.Fatalf("unprojected post after consumed sync = %q, %v; want live pending body", string(got), err)
	}
}

// TestProjectMailboxProjection_IgnoresEmptyContentReadEvent reproduces the
// #633 root cause: a burst of racy mailbox_projection_read events for the
// same message_id, where a later event's payload carries empty content
// (e.g. its shadow recorder observed a torn/truncated file mid rewrite).
// Replaying such a journal must not let the empty read permanently zero
// out the projected body.
func TestProjectMailboxProjection_IgnoresEmptyContentReadEvent(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.July, 10, 15, 1, 50, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	filename := "20260710-000149-s7c1c-ra364-from-orchestrator-to-guardian.md"
	readRel := filepath.Join("read", filename)
	appendMailboxEventForTest(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID: filename,
		From:      "orchestrator",
		To:        "guardian",
		Path:      readRel,
		Content:   "full correct body",
	}, now.Add(16*time.Second))

	for i := 0; i < 4; i++ {
		appendMailboxEventForTest(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
			MessageID: filename,
			From:      "orchestrator",
			To:        "guardian",
			Path:      readRel,
			Content:   "",
		}, now.Add(time.Duration(20+i)*time.Second))
	}

	projected, ok, err := ProjectMailboxProjection(sessionDir)
	if err != nil {
		t.Fatalf("ProjectMailboxProjection() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxProjection() ok = false, want true")
	}
	if got := projected.Read[pathKey(readRel)]; got.Content != "full correct body" {
		t.Fatalf("read projection content = %q, want full correct body (empty read events must not clobber it)", got.Content)
	}

	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(sessionDir, readRel))
	if err != nil {
		t.Fatalf("ReadFile(projected read file): %v", err)
	}
	if string(got) != "full correct body" {
		t.Fatalf("projected read file content = %q, want full correct body", string(got))
	}
}

// TestProjectMailboxProjection_FirstEmptyReadEventDoesNotDropMessage guards
// against two regressions found across two rounds of review of the #633
// fix:
//  1. (round 2) When the FIRST-ever read event for a message carries empty
//     content, unconditionally deleting the inbox entry dropped the message
//     from both inbox and read projections at once, so the cleanup pass in
//     syncDesiredMailboxFiles deleted its on-disk file outright -- worse
//     than the original #633 bug, which at least left a visible (if
//     0-byte) file.
//  2. (round 3) Fixing #1 by simply leaving the inbox entry untouched
//     instead introduced a NEW hazard: on the non-owner direct-pop path,
//     ArchiveInboxMessage already moves the message to read/ via a raw
//     rename before any journal event exists for it. Leaving the stale
//     inbox entry in `desired` made syncDesiredMailboxFiles write the
//     message back into inbox/, resurrecting an already-archived message
//     as unread and re-consumable -- a duplicate-processing hazard.
//
// The correct behavior (tombstoning): the inbox entry is removed (no
// resurrection), no bogus empty Read entry is fabricated, and whatever
// real file already exists at the read path is left untouched by the
// cleanup pass rather than deleted. A subsequent genuine, non-empty read
// event must still complete the transition normally.
func TestProjectMailboxProjection_FirstEmptyReadEventDoesNotDropMessage(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.July, 10, 15, 40, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	filename := "20260710-154000-s7c1c-rfirst-from-orchestrator-to-guardian.md"
	inboxRel := filepath.Join("inbox", "guardian", filename)
	readRel := filepath.Join("read", filename)
	appendMailboxEventForTest(t, writer, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: filename,
		From:      "orchestrator",
		To:        "guardian",
		Path:      inboxRel,
		Content:   "delivered body",
	}, now.Add(time.Second))
	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection(after delivered) error = %v", err)
	}

	// Simulate the non-owner direct-pop path: the message has already been
	// archived via a raw filesystem rename (ArchiveInboxMessage) before any
	// journal read event exists for it. The physical inbox file is gone;
	// the physical read file holds the real, correct content.
	if err := os.Remove(filepath.Join(sessionDir, inboxRel)); err != nil {
		t.Fatalf("simulate raw archive rename (remove inbox file): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sessionDir, "read"), 0o700); err != nil {
		t.Fatalf("MkdirAll(read): %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, readRel), []byte("delivered body"), 0o600); err != nil {
		t.Fatalf("simulate raw archive rename (write read file): %v", err)
	}

	// First-ever read event for this message: content is empty (e.g. a
	// racy shadow-recorder observation), with no prior non-empty read.
	appendMailboxEventForTest(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID: filename,
		From:      "orchestrator",
		To:        "guardian",
		Path:      readRel,
		Content:   "",
	}, now.Add(2*time.Second))

	projected, ok, err := ProjectMailboxProjection(sessionDir)
	if err != nil {
		t.Fatalf("ProjectMailboxProjection() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxProjection() ok = false, want true")
	}
	if _, exists := projected.Inbox[pathKey(inboxRel)]; exists {
		t.Fatalf("inbox projection resurrected after first empty read: %#v", projected.Inbox[pathKey(inboxRel)])
	}
	if _, exists := projected.Read[pathKey(readRel)]; exists {
		t.Fatalf("read projection fabricated from empty first read event: %#v", projected.Read[pathKey(readRel)])
	}

	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection(after first empty read) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, inboxRel)); !os.IsNotExist(err) {
		t.Fatalf("inbox file resurrected after first empty read: err=%v (message re-consumable, duplicate-processing hazard)", err)
	}
	if got, err := os.ReadFile(filepath.Join(sessionDir, readRel)); err != nil || string(got) != "delivered body" {
		t.Fatalf("read file after first empty read = %q, %v; want delivered body preserved (tombstoned, not deleted)", string(got), err)
	}

	// A genuine, non-empty read event now arrives and must complete the
	// transition normally: read entry populated from the journal as usual.
	appendMailboxEventForTest(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID: filename,
		From:      "orchestrator",
		To:        "guardian",
		Path:      readRel,
		Content:   "delivered body",
	}, now.Add(3*time.Second))

	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection(after real read) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, inboxRel)); !os.IsNotExist(err) {
		t.Fatalf("inbox file present after genuine read completed: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(sessionDir, readRel)); err != nil || string(got) != "delivered body" {
		t.Fatalf("read file after genuine read = %q, %v; want delivered body", string(got), err)
	}
}

// TestWriteFileAtomic_NeverLeavesTruncatedFileVisible guards against
// regressing to a truncate-in-place write for projected mailbox files: a
// concurrent reader must always see either the full old content or the
// full new content on the target path, never an empty/partial write.
func TestWriteFileAtomic_NeverLeavesTruncatedFileVisible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "read", "20260710-000149-s7c1c-ra364-from-orchestrator-to-guardian.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := writeFileAtomic(path, []byte("first version"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic(first): %v", err)
	}

	stop := make(chan struct{})
	sawTruncated := make(chan bool, 1)
	go func() {
		defer close(sawTruncated)
		for {
			select {
			case <-stop:
				return
			default:
				content, err := os.ReadFile(path)
				if err == nil && len(content) == 0 {
					sawTruncated <- true
					return
				}
			}
		}
	}()

	for i := 0; i < 200; i++ {
		if err := writeFileAtomic(path, []byte("rewritten version"), 0o600); err != nil {
			t.Fatalf("writeFileAtomic(rewrite %d): %v", i, err)
		}
	}
	close(stop)
	if truncated, ok := <-sawTruncated; ok && truncated {
		t.Fatal("concurrent reader observed a 0-byte file during atomic rewrite")
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(path) {
			t.Fatalf("leftover temp file after atomic write: %s", entry.Name())
		}
	}
}

func appendMailboxEventForTest(t *testing.T, writer *journal.Writer, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, visibility, payload, now); err != nil {
		t.Fatalf("AppendEvent(%s): %v", eventType, err)
	}
}
