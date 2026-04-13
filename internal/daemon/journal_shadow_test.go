package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestRecordShadowMailboxPathEvent_AppendsOperatorVisibleRead(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 17, 30, 0, 0, time.UTC)

	manager := journal.NewManager("ctx-main", 4242)
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)

	eventPath := filepath.Join(sessionDir, "read", "20260414-173000-r1234-from-orchestrator-to-worker.md")
	recordShadowMailboxPathEvent(eventPath, "compatibility_mailbox_read", journal.VisibilityOperatorVisible, now)

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("journal.Replay() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("journal.Replay() returned %d events, want 3", len(events))
	}
	if events[2].Type != "compatibility_mailbox_read" {
		t.Fatalf("events[2].Type = %q, want compatibility_mailbox_read", events[2].Type)
	}
	if events[2].Visibility != journal.VisibilityOperatorVisible {
		t.Fatalf("events[2].Visibility = %q, want %q", events[2].Visibility, journal.VisibilityOperatorVisible)
	}

	var payload map[string]string
	if err := json.Unmarshal(events[2].Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload): %v", err)
	}
	if payload["path"] != filepath.Join("read", "20260414-173000-r1234-from-orchestrator-to-worker.md") {
		t.Fatalf("payload[path] = %q", payload["path"])
	}
	if payload["from"] != "orchestrator" || payload["to"] != "worker" {
		t.Fatalf("payload = %#v, want from=orchestrator to=worker", payload)
	}
}

func TestShadowSessionFromEventPath(t *testing.T) {
	sessionDir := filepath.Join(string(os.PathSeparator), "tmp", "ctx-main", "review-session")
	eventPath := filepath.Join(sessionDir, "post", "20260414-173100-r5678-from-worker-to-orchestrator.md")

	gotDir, gotSession, ok := shadowSessionFromEventPath(eventPath)
	if !ok {
		t.Fatal("shadowSessionFromEventPath() ok = false, want true")
	}
	if gotDir != sessionDir {
		t.Fatalf("shadowSessionFromEventPath() dir = %q, want %q", gotDir, sessionDir)
	}
	if gotSession != "review-session" {
		t.Fatalf("shadowSessionFromEventPath() session = %q, want review-session", gotSession)
	}
}
