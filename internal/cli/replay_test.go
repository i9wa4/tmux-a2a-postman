package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestRunReplay_MailboxSurfaceIsReadOnly(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-replay"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	liveInboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(liveInboxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(liveInboxDir): %v", err)
	}
	livePath := filepath.Join(liveInboxDir, "legacy.md")
	if err := os.WriteFile(livePath, []byte("legacy inbox copy\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(livePath): %v", err)
	}

	now := time.Date(2026, time.April, 14, 6, 5, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, contextID, sessionName, 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	if _, err := writer.AppendEvent(
		"compatibility_mailbox_delivered",
		journal.VisibilityCompatibilityMailbox,
		journal.MailboxEventPayload{
			MessageID: "20260414-060501-r1111-from-orchestrator-to-worker.md",
			From:      "orchestrator",
			To:        "worker",
			Path:      filepath.Join("inbox", "worker", "20260414-060501-r1111-from-orchestrator-to-worker.md"),
			Content:   "shadow-only message body\n",
		},
		now.Add(time.Second),
	); err != nil {
		t.Fatalf("AppendEvent(delivered): %v", err)
	}

	t.Setenv("POSTMAN_HOME", baseDir)

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunReplay([]string{
			"--context-id", contextID,
			"--session", sessionName,
			"--surface", "mailbox",
		})
	})
	if err != nil {
		t.Fatalf("RunReplay() error = %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, `"surface": "mailbox"`) {
		t.Fatalf("stdout missing mailbox surface: %q", stdout)
	}
	if !strings.Contains(stdout, `20260414-060501-r1111-from-orchestrator-to-worker.md`) {
		t.Fatalf("stdout missing projected message path: %q", stdout)
	}
	if strings.Contains(stdout, "shadow-only message body") {
		t.Fatalf("stdout leaked mailbox content: %q", stdout)
	}

	gotLive, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatalf("ReadFile(livePath): %v", err)
	}
	if string(gotLive) != "legacy inbox copy\n" {
		t.Fatalf("live inbox content = %q, want legacy copy unchanged", string(gotLive))
	}
	entries, err := os.ReadDir(liveInboxDir)
	if err != nil {
		t.Fatalf("ReadDir(liveInboxDir): %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "legacy.md" {
		t.Fatalf("live inbox entries = %#v, want only legacy.md", entries)
	}
}
