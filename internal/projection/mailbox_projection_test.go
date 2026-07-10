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
