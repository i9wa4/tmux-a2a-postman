package projection

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestProjectCompatibilityMailbox_FourDirectoryLifecycle(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 3, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	appendMailboxEventForTest(t, writer, "compatibility_mailbox_posted", journal.VisibilityCompatibilityMailbox, journal.MailboxEventPayload{
		MessageID: "20260414-030001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("post", "20260414-030001-r1111-from-orchestrator-to-worker.md"),
		Content:   "queued body",
	}, now.Add(1*time.Second))
	appendMailboxEventForTest(t, writer, "compatibility_mailbox_posted", journal.VisibilityCompatibilityMailbox, journal.MailboxEventPayload{
		MessageID: "20260414-030002-r2222-from-orchestrator-to-critic.md",
		From:      "orchestrator",
		To:        "critic",
		Path:      filepath.Join("post", "20260414-030002-r2222-from-orchestrator-to-critic.md"),
		Content:   "dead-letter body",
	}, now.Add(2*time.Second))
	appendMailboxEventForTest(t, writer, "compatibility_mailbox_post_consumed", journal.VisibilityCompatibilityMailbox, journal.MailboxEventPayload{
		MessageID: "20260414-030001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("post", "20260414-030001-r1111-from-orchestrator-to-worker.md"),
	}, now.Add(3*time.Second))
	appendMailboxEventForTest(t, writer, "compatibility_mailbox_delivered", journal.VisibilityCompatibilityMailbox, journal.MailboxEventPayload{
		MessageID: "20260414-030001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("inbox", "worker", "20260414-030001-r1111-from-orchestrator-to-worker.md"),
		Content:   "queued body",
	}, now.Add(4*time.Second))
	appendMailboxEventForTest(t, writer, "compatibility_mailbox_read", journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID: "20260414-030001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("read", "20260414-030001-r1111-from-orchestrator-to-worker.md"),
		Content:   "queued body",
	}, now.Add(5*time.Second))
	appendMailboxEventForTest(t, writer, "compatibility_mailbox_dead_lettered", journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID:  "20260414-030002-r2222-from-orchestrator-to-critic-dl-routing-denied.md",
		From:       "orchestrator",
		To:         "critic",
		Path:       filepath.Join("dead-letter", "20260414-030002-r2222-from-orchestrator-to-critic-dl-routing-denied.md"),
		SourcePath: filepath.Join("post", "20260414-030002-r2222-from-orchestrator-to-critic.md"),
		Content:    "dead-letter body",
	}, now.Add(6*time.Second))

	projected, ok, err := ProjectCompatibilityMailbox(sessionDir)
	if err != nil {
		t.Fatalf("ProjectCompatibilityMailbox() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectCompatibilityMailbox() ok = false, want true")
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

func TestProjectCompatibilityMailbox_ControlPlaneOnlyExcluded(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 4, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	appendMailboxEventForTest(t, writer, "compatibility_mailbox_posted", journal.VisibilityControlPlaneOnly, journal.MailboxEventPayload{
		MessageID: "20260414-040001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("post", "20260414-040001-r1111-from-orchestrator-to-worker.md"),
		Content:   "hidden body",
	}, now.Add(time.Second))

	projected, ok, err := ProjectCompatibilityMailbox(sessionDir)
	if err != nil {
		t.Fatalf("ProjectCompatibilityMailbox() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectCompatibilityMailbox() ok = false, want true")
	}
	if len(projected.Post) != 0 || len(projected.Inbox) != 0 || len(projected.Read) != 0 || len(projected.DeadLetter) != 0 {
		t.Fatalf("control-plane event leaked into compatibility projection: %#v", projected)
	}
}

func TestSyncCompatibilityMailbox_GenerationQuarantine(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 5, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	appendMailboxEventForTest(t, writer, "compatibility_mailbox_posted", journal.VisibilityCompatibilityMailbox, journal.MailboxEventPayload{
		MessageID: "20260414-050001-r1111-from-orchestrator-to-worker.md",
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("post", "20260414-050001-r1111-from-orchestrator-to-worker.md"),
		Content:   "queued body",
	}, now.Add(time.Second))

	if err := SyncCompatibilityMailbox(sessionDir); err != nil {
		t.Fatalf("SyncCompatibilityMailbox() error = %v", err)
	}

	if _, _, err := journal.ResolveSession(sessionDir, "review", journal.ResolutionExplicitRebind, now.Add(2*time.Second)); err != nil {
		t.Fatalf("ResolveSession(explicit rebind) error = %v", err)
	}
	if _, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 102, now.Add(3*time.Second)); err != nil {
		t.Fatalf("OpenShadowWriter(rebind) error = %v", err)
	}

	if err := SyncCompatibilityMailbox(sessionDir); err != nil {
		t.Fatalf("SyncCompatibilityMailbox(rebind) error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(sessionDir, "snapshot", "quarantine", "*", "post", "20260414-050001-r1111-from-orchestrator-to-worker.md"))
	if err != nil {
		t.Fatalf("Glob(quarantine): %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("quarantine matches = %d, want 1", len(matches))
	}
}

func TestSyncCompatibilityMailbox_PreservesUnprojectedPostFiles(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 5, 30, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	appendMailboxEventForTest(t, writer, "compatibility_mailbox_posted", journal.VisibilityCompatibilityMailbox, journal.MailboxEventPayload{
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

	if err := SyncCompatibilityMailbox(sessionDir); err != nil {
		t.Fatalf("SyncCompatibilityMailbox() error = %v", err)
	}
	got, err := os.ReadFile(unprojectedPath)
	if err != nil {
		t.Fatalf("unprojected post file was removed: %v", err)
	}
	if string(got) != "live pending body" {
		t.Fatalf("unprojected post content = %q, want live pending body", string(got))
	}
}

func appendMailboxEventForTest(t *testing.T, writer *journal.Writer, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, visibility, payload, now); err != nil {
		t.Fatalf("AppendEvent(%s): %v", eventType, err)
	}
}
