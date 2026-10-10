package projection

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/store"
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

// writeTestSessionState writes a minimal journal.SessionState directly to
// its expected path, bypassing the full lease/journal pipeline: the
// quarantine/gate tests below only need loadCurrentSessionState to see a
// matching (or mismatched) sessionKey/generation, not a real lease.
func writeTestSessionState(t *testing.T, sessionDir, sessionKey string, generation int) {
	t.Helper()
	state := journal.SessionState{SessionKey: sessionKey, Generation: generation}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal(SessionState): %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(journal.SessionStatePath(sessionDir)), 0o700); err != nil {
		t.Fatalf("MkdirAll(session state dir): %v", err)
	}
	if err := os.WriteFile(journal.SessionStatePath(sessionDir), data, 0o600); err != nil {
		t.Fatalf("WriteFile(session state): %v", err)
	}
}

// ---------------------------------------------------------------------
// P2-1R (supersedes P2-1, NOT APPROVED 20261002-125147-sfb93-r833b):
// disposable-fixture tests for the session-level mailbox roots gate
// wired into SyncMailboxProjection's generation-transition path. Item 6
// (a), (b), (c), (e) below; item 6(d) (symlink rejection) and the gate
// primitive's own shared/exclusive exclusion tests live in
// internal/store/mailbox_roots_test.go next to the primitive itself.
// ---------------------------------------------------------------------

// TestQuarantineGateBlocksUntilSharedHolderReleases is P2-1R item 6(a): a
// holder takes the roots gate SHARED (the stand-in for a future fenced
// writer or claim); a test seam fires just before the sync path requests
// EXCLUSIVE, giving a deterministic synchronization point instead of a
// sleep. While the holder holds, sync must not have returned and the live
// inbox root (and the in-flight file inside it) must not have been
// renamed. The holder then writes its message file INSIDE its shared
// hold, after a controlled signal, and releases; the file must end up
// intact ONLY at generation-1/inbox/<node>/<file>, and be absent from
// live -- not "either location" (the defect NOT APPROVED's G-4/P1-F4
// found in the superseded P2-1 fixture).
func TestQuarantineGateBlocksUntilSharedHolderReleases(t *testing.T) {
	sessionDir := t.TempDir()
	const recipient = "node-a"
	inboxDir := filepath.Join(sessionDir, "inbox", recipient)
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	msgPath := filepath.Join(inboxDir, "20260414-030001-r1111-from-orchestrator-to-node-a.md")

	if err := writeMailboxProjectionMarker(sessionDir, mailboxProjectionMarker{SessionKey: "s1", Generation: 1}); err != nil {
		t.Fatalf("writeMailboxProjectionMarker: %v", err)
	}
	writeTestSessionState(t, sessionDir, "s1", 2)

	holding := make(chan struct{})
	writeNow := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- store.WithMailboxRootsShared(sessionDir, func() error {
			close(holding)
			<-writeNow
			if werr := os.WriteFile(msgPath, []byte("in-flight message"), 0o600); werr != nil {
				return werr
			}
			<-release
			return nil
		})
	}()
	<-holding

	reachedGate := make(chan struct{})
	syncDone := make(chan error, 1)
	go func() {
		syncDone <- syncMailboxProjectionSeam(sessionDir, func() { close(reachedGate) }, nil)
	}()
	<-reachedGate

	close(writeNow)
	// Let the holder's write complete while it still holds the shared
	// gate; this does not gate correctness below (every assertion in
	// this window checks real filesystem/channel state, not timing), it
	// only improves the odds of exercising real contention before we
	// check that nothing has moved yet.
	time.Sleep(10 * time.Millisecond)

	select {
	case <-syncDone:
		t.Fatal("sync returned while the shared holder still held the roots gate")
	default:
	}
	if _, err := os.Stat(inboxDir); err != nil {
		t.Fatalf("live inbox dir missing while the shared holder still holds the gate: %v", err)
	}
	if _, err := os.Stat(msgPath); err != nil {
		t.Fatalf("in-flight message missing from the live inbox while the shared holder still holds the gate: %v", err)
	}

	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder WithMailboxRootsShared: %v", err)
	}
	if err := <-syncDone; err != nil {
		t.Fatalf("syncMailboxProjectionSeam: %v", err)
	}

	if _, err := os.Stat(msgPath); !os.IsNotExist(err) {
		t.Fatalf("Stat(live msgPath) = %v, want IsNotExist (file must be gone from the live inbox)", err)
	}
	quarantinedPath := filepath.Join(sessionDir, "snapshot", "quarantine", "generation-1", "inbox", recipient, filepath.Base(msgPath))
	data, err := os.ReadFile(quarantinedPath)
	if err != nil {
		t.Fatalf("ReadFile(quarantined message): %v", err)
	}
	if string(data) != "in-flight message" {
		t.Fatalf("quarantined message content = %q, want %q", data, "in-flight message")
	}
}

// TestQuarantineConcurrentTransitionsConverge is P2-1R item 6(b): two
// concurrent generation transitions racing on the same old marker must
// converge so exactly one moves the roots; the other, serialized behind
// the same exclusive gate, must observe the already-updated marker and
// move nothing, and no snapshot may be deleted or duplicated with a
// numbered suffix.
func TestQuarantineConcurrentTransitionsConverge(t *testing.T) {
	sessionDir := t.TempDir()
	const recipient = "node-a"
	inboxDir := filepath.Join(sessionDir, "inbox", recipient)
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	msgPath := filepath.Join(inboxDir, "20260414-030001-r1111-from-orchestrator-to-node-a.md")
	if err := os.WriteFile(msgPath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := writeMailboxProjectionMarker(sessionDir, mailboxProjectionMarker{SessionKey: "s1", Generation: 1}); err != nil {
		t.Fatalf("writeMailboxProjectionMarker: %v", err)
	}
	writeTestSessionState(t, sessionDir, "s1", 2)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = syncMailboxProjectionSeam(sessionDir, nil, nil)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("transition %d: %v", i, err)
		}
	}

	quarantinedPath := filepath.Join(sessionDir, "snapshot", "quarantine", "generation-1", "inbox", recipient, filepath.Base(msgPath))
	data, err := os.ReadFile(quarantinedPath)
	if err != nil {
		t.Fatalf("ReadFile(quarantined message): %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("quarantined content = %q, want %q", data, "payload")
	}

	suffixed := filepath.Join(sessionDir, "snapshot", "quarantine", "generation-1", "inbox.1")
	if _, err := os.Stat(suffixed); !os.IsNotExist(err) {
		t.Fatalf("Stat(inbox.1) = %v, want IsNotExist (the second transition must have moved nothing, having observed the already-updated marker)", err)
	}
}

// TestQuarantinePartialFailureIsNonDestructiveAndRetrySafe is P2-1R item
// 6(c): a failure injected on the second root's rename must leave the
// marker-relevant state such that the first root's snapshot survives
// untouched, the root that failed to move is left exactly as it was
// (nothing lost), and a retry after new activity lands the new content in
// a numbered-suffix destination rather than overwriting or merging into
// the existing snapshot.
func TestQuarantinePartialFailureIsNonDestructiveAndRetrySafe(t *testing.T) {
	sessionDir := t.TempDir()
	const recipient = "node-a"
	postDir := filepath.Join(sessionDir, "post")
	inboxDir := filepath.Join(sessionDir, "inbox", recipient)
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(post): %v", err)
	}
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(inbox): %v", err)
	}
	postFileA := filepath.Join(postDir, "a.md")
	if err := os.WriteFile(postFileA, []byte("content-A"), 0o600); err != nil {
		t.Fatalf("WriteFile(post/a.md): %v", err)
	}
	inboxFile := filepath.Join(inboxDir, "msg.md")
	if err := os.WriteFile(inboxFile, []byte("inbox-content"), 0o600); err != nil {
		t.Fatalf("WriteFile(inbox msg): %v", err)
	}

	marker := mailboxProjectionMarker{SessionKey: "s1", Generation: 1}

	failInbox := quarantineOps{rename: func(oldpath, newpath string) error {
		if filepath.Base(oldpath) == "inbox" {
			return fmt.Errorf("injected rename failure for inbox")
		}
		return os.Rename(oldpath, newpath)
	}}

	if err := quarantineMailboxProjectionRootsWithOps(sessionDir, marker, failInbox); err == nil {
		t.Fatal("quarantineMailboxProjectionRootsWithOps: want error from the injected inbox rename failure, got nil")
	}

	quarantineRoot := filepath.Join(sessionDir, "snapshot", "quarantine", "generation-1")

	data, err := os.ReadFile(filepath.Join(quarantineRoot, "post", "a.md"))
	if err != nil || string(data) != "content-A" {
		t.Fatalf("first snapshot (post) = %q, %v, want %q, nil", data, err, "content-A")
	}
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir(live post): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("live post/ = %d entries, want 0 (recreated empty after its successful move)", len(entries))
	}

	liveData, err := os.ReadFile(inboxFile)
	if err != nil || string(liveData) != "inbox-content" {
		t.Fatalf("live inbox file = %q, %v, want %q, nil (must survive the failed rename untouched)", liveData, err, "inbox-content")
	}
	if _, err := os.Stat(filepath.Join(quarantineRoot, "inbox")); !os.IsNotExist(err) {
		t.Fatalf("Stat(quarantine inbox) = %v, want IsNotExist (inbox was never moved)", err)
	}

	postFileB := filepath.Join(postDir, "b.md")
	if err := os.WriteFile(postFileB, []byte("content-B"), 0o600); err != nil {
		t.Fatalf("WriteFile(post/b.md): %v", err)
	}

	if err := quarantineMailboxProjectionRootsWithOps(sessionDir, marker, osQuarantineOps); err != nil {
		t.Fatalf("retry quarantineMailboxProjectionRootsWithOps: %v", err)
	}

	data, err = os.ReadFile(filepath.Join(quarantineRoot, "post", "a.md"))
	if err != nil || string(data) != "content-A" {
		t.Fatalf("first snapshot (post) after retry = %q, %v, want unchanged %q, nil", data, err, "content-A")
	}
	data, err = os.ReadFile(filepath.Join(quarantineRoot, "post.1", "b.md"))
	if err != nil || string(data) != "content-B" {
		t.Fatalf("retry snapshot (post.1) = %q, %v, want %q, nil", data, err, "content-B")
	}
	data, err = os.ReadFile(filepath.Join(quarantineRoot, "inbox", recipient, "msg.md"))
	if err != nil || string(data) != "inbox-content" {
		t.Fatalf("retried inbox snapshot = %q, %v, want %q, nil", data, err, "inbox-content")
	}
}

// TestQuarantineCreatesNoRecipientAdmissionState is P2-1R item 6(e):
// quarantine must never create a per-recipient admission directory under
// mailbox-locks/ -- it takes no recipient fence at all (P2-1R item 3).
func TestQuarantineCreatesNoRecipientAdmissionState(t *testing.T) {
	sessionDir := t.TempDir()
	const recipient = "node-a"
	inboxDir := filepath.Join(sessionDir, "inbox", recipient)
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, "msg.md"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	marker := mailboxProjectionMarker{SessionKey: "s1", Generation: 1}
	if err := quarantineMailboxProjectionRoots(sessionDir, marker); err != nil {
		t.Fatalf("quarantineMailboxProjectionRoots: %v", err)
	}

	locksRoot := filepath.Join(sessionDir, "mailbox-locks")
	entries, err := os.ReadDir(locksRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("ReadDir(mailbox-locks): %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Fatalf("quarantine created recipient admission dir %q under mailbox-locks/", e.Name())
		}
	}
}

// ---------------------------------------------------------------------
// P2-1R rework 2 (R1-1/R1-2, NOT APPROVED 20261002-173147-sfb93-rd5f6):
// session state must be re-read fresh under the gate, both on the
// exclusive transition path (R1-1) and the shared fast path (R1-2).
// Tests (f) and (g) below need a real marker-writing sync body, so unlike
// (a)/(b)/(e) they set up genuine journal state via
// journal.OpenShadowWriter/journal.ResolveSession rather than the
// synthetic writeTestSessionState helper.
// ---------------------------------------------------------------------

// TestQuarantineRereadsSessionStateUnderExclusiveGate is P2-1R item 6(f):
// a caller's fast path observes a stale marker and decides a transition
// is needed; a test seam fires immediately before the exclusive gate is
// requested; the session is advanced past the generation the fast path
// originally saw, simulating R1-1's exact race (the session moves on
// while a transition waits for the gate). The final marker must equal
// the NEWEST generation (re-read fresh under the gate), not whatever was
// observed before it, and a message delivered under that newest
// generation must end up live, never swept into the stale snapshot.
func TestQuarantineRereadsSessionStateUnderExclusiveGate(t *testing.T) {
	sessionDir := t.TempDir()
	const recipient = "node-a"
	const tmuxSessionName = "review"
	t0 := time.Date(2026, time.April, 14, 5, 0, 0, 0, time.UTC)

	writer1, err := journal.OpenShadowWriter(sessionDir, "ctx-f", tmuxSessionName, 101, t0)
	if err != nil {
		t.Fatalf("OpenShadowWriter (gen1): %v", err)
	}
	sessionKey, gen1, ok := CurrentSessionIdentity(sessionDir)
	if !ok || gen1 != 1 {
		t.Fatalf("CurrentSessionIdentity after gen1 open = (%q, %d, %v), want (_, 1, true)", sessionKey, gen1, ok)
	}
	appendMailboxEventForTest(t, writer1, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "old.md",
		From:      "orchestrator",
		To:        recipient,
		Path:      filepath.Join("inbox", recipient, "old.md"),
		Content:   "old-gen-1-message",
	}, t0.Add(1*time.Second))

	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("initial SyncMailboxProjection (gen1): %v", err)
	}
	marker, ok := readMailboxProjectionMarker(sessionDir)
	if !ok || marker.SessionKey != sessionKey || marker.Generation != 1 {
		t.Fatalf("marker after initial sync = %+v, ok=%v, want {%q 1}, true", marker, ok, sessionKey)
	}
	oldPath := filepath.Join(sessionDir, "inbox", recipient, "old.md")
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("old.md not written by the initial sync: %v", err)
	}

	// Advance the session to generation 2 first, so this call's fast
	// path observes a mismatch (marker=1, state=2) and decides a
	// transition is needed, exactly like a real caller would.
	t1 := t0.Add(2 * time.Second)
	if _, _, err := journal.ResolveSession(sessionDir, tmuxSessionName, journal.ResolutionExplicitRebind, t1); err != nil {
		t.Fatalf("ResolveSession(rebind to 2): %v", err)
	}

	seamFired := false
	err = syncMailboxProjectionSeam(sessionDir, func() {
		// R1-1's race: the session advances AGAIN, past what the fast
		// path already saw, while this caller is about to request the
		// exclusive gate.
		t2 := t1.Add(1 * time.Second)
		if _, _, rerr := journal.ResolveSession(sessionDir, tmuxSessionName, journal.ResolutionExplicitRebind, t2); rerr != nil {
			t.Fatalf("ResolveSession(rebind to 3): %v", rerr)
		}
		writer3, werr := journal.OpenShadowWriter(sessionDir, "ctx-f", tmuxSessionName, 101, t2)
		if werr != nil {
			t.Fatalf("OpenShadowWriter (gen3): %v", werr)
		}
		appendMailboxEventForTest(t, writer3, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
			MessageID: "new.md",
			From:      "orchestrator",
			To:        recipient,
			Path:      filepath.Join("inbox", recipient, "new.md"),
			Content:   "new-gen-3-message",
		}, t2.Add(1*time.Second))
		seamFired = true
	}, nil)
	if err != nil {
		t.Fatalf("syncMailboxProjectionSeam: %v", err)
	}
	if !seamFired {
		t.Fatal("seam never fired")
	}

	finalMarker, ok := readMailboxProjectionMarker(sessionDir)
	if !ok || finalMarker.Generation != 3 {
		t.Fatalf("final marker = %+v, ok=%v, want Generation=3 (must reflect the state re-read under the gate, not a stale pre-gate value)", finalMarker, ok)
	}
	newPath := filepath.Join(sessionDir, "inbox", recipient, "new.md")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new-generation message missing from the live inbox: %v", err)
	}
	oldQuarantined := filepath.Join(sessionDir, "snapshot", "quarantine", "generation-1", "inbox", recipient, "old.md")
	if _, err := os.ReadFile(oldQuarantined); err != nil {
		t.Fatalf("old message not found intact in the generation-1 snapshot: %v", err)
	}
}

// TestFastPathSharedHoldBlocksConcurrentTransition is P2-1R item 6(g): a
// fast-path sync (marker already matches) takes the gate SHARED and
// pauses there (seam) before running its body; a concurrent transition
// racing in must not return, and must not rename the live inbox, while
// that shared hold is active. After the fast path releases, the
// transition proceeds and the final marker reflects the newer
// generation, with no marker regression.
func TestFastPathSharedHoldBlocksConcurrentTransition(t *testing.T) {
	sessionDir := t.TempDir()
	const recipient = "node-a"
	const tmuxSessionName = "review"
	t0 := time.Date(2026, time.April, 14, 6, 0, 0, 0, time.UTC)

	writer1, err := journal.OpenShadowWriter(sessionDir, "ctx-g", tmuxSessionName, 101, t0)
	if err != nil {
		t.Fatalf("OpenShadowWriter (gen1): %v", err)
	}
	sessionKey, gen1, ok := CurrentSessionIdentity(sessionDir)
	if !ok || gen1 != 1 {
		t.Fatalf("CurrentSessionIdentity after gen1 open = (%q, %d, %v), want (_, 1, true)", sessionKey, gen1, ok)
	}
	appendMailboxEventForTest(t, writer1, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "old.md",
		From:      "orchestrator",
		To:        recipient,
		Path:      filepath.Join("inbox", recipient, "old.md"),
		Content:   "old-gen-1-message",
	}, t0.Add(1*time.Second))

	if err := SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("initial SyncMailboxProjection (gen1): %v", err)
	}
	marker, ok := readMailboxProjectionMarker(sessionDir)
	if !ok || marker.SessionKey != sessionKey || marker.Generation != 1 {
		t.Fatalf("marker after initial sync = %+v, ok=%v, want {%q 1}, true", marker, ok, sessionKey)
	}
	oldPath := filepath.Join(sessionDir, "inbox", recipient, "old.md")
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("old.md not written by the initial sync: %v", err)
	}

	paused := make(chan struct{})
	resume := make(chan struct{})
	fastPathDone := make(chan error, 1)
	go func() {
		fastPathDone <- syncMailboxProjectionSeam(sessionDir, nil, func() {
			close(paused)
			<-resume
		})
	}()
	<-paused

	// While the fast path holds the gate SHARED, advance to generation 2
	// and deliver new content there.
	t1 := t0.Add(2 * time.Second)
	if _, _, err := journal.ResolveSession(sessionDir, tmuxSessionName, journal.ResolutionExplicitRebind, t1); err != nil {
		t.Fatalf("ResolveSession(rebind to 2): %v", err)
	}
	writer2, err := journal.OpenShadowWriter(sessionDir, "ctx-g", tmuxSessionName, 101, t1)
	if err != nil {
		t.Fatalf("OpenShadowWriter (gen2): %v", err)
	}
	appendMailboxEventForTest(t, writer2, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "new.md",
		From:      "orchestrator",
		To:        recipient,
		Path:      filepath.Join("inbox", recipient, "new.md"),
		Content:   "new-gen-2-message",
	}, t1.Add(1*time.Second))

	reachedExclusive := make(chan struct{})
	transitionDone := make(chan error, 1)
	go func() {
		transitionDone <- syncMailboxProjectionSeam(sessionDir, func() { close(reachedExclusive) }, nil)
	}()
	<-reachedExclusive

	select {
	case <-transitionDone:
		t.Fatal("concurrent transition returned while the fast-path shared hold was still active")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("live old.md missing while the fast-path shared hold is still active (must not have been renamed yet): %v", err)
	}

	close(resume)
	if err := <-fastPathDone; err != nil {
		t.Fatalf("fast path sync: %v", err)
	}
	if err := <-transitionDone; err != nil {
		t.Fatalf("transition sync: %v", err)
	}

	finalMarker, ok := readMailboxProjectionMarker(sessionDir)
	if !ok || finalMarker.Generation != 2 {
		t.Fatalf("final marker = %+v, ok=%v, want Generation=2 (no marker regression after the fast path released)", finalMarker, ok)
	}
	newPath := filepath.Join(sessionDir, "inbox", recipient, "new.md")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new-generation message missing from the live inbox: %v", err)
	}
	oldQuarantined := filepath.Join(sessionDir, "snapshot", "quarantine", "generation-1", "inbox", recipient, "old.md")
	if _, err := os.ReadFile(oldQuarantined); err != nil {
		t.Fatalf("old message not found intact in the generation-1 snapshot: %v", err)
	}
}
