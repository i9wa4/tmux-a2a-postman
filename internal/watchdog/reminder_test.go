package watchdog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// Issue #125: Tests verifying capture failure does not suppress idle reminder.

func TestSendIdleReminder_CreatesPostFile(t *testing.T) {
	tmpDir := t.TempDir()
	postDir := filepath.Join(tmpDir, "post")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		t.Fatalf("creating post dir: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.UINode = "messenger"
	activity := PaneActivity{
		PaneID:           "%99",
		LastActivityTime: time.Now().Add(-10 * time.Minute),
	}

	err := SendIdleReminder(cfg, "%99", tmpDir, "test-context", "messenger", activity)
	if err != nil {
		t.Fatalf("SendIdleReminder failed: %v", err)
	}

	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("reading post dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 post file, got %d", len(entries))
	}
}

func TestCaptureFailureDoesNotSuppressReminder(t *testing.T) {
	// Verify: CapturePane with invalid pane fails, but SendIdleReminder still succeeds.
	// This is the core invariant of Issue #125.
	tmpDir := t.TempDir()
	postDir := filepath.Join(tmpDir, "post")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		t.Fatalf("creating post dir: %v", err)
	}
	captureDir := filepath.Join(tmpDir, "capture")

	activity := PaneActivity{
		PaneID:           "%invalid-pane",
		LastActivityTime: time.Now().Add(-10 * time.Minute),
	}

	// Step 1: CapturePane fails (invalid pane ID).
	_, captureErr := CapturePane(activity.PaneID, captureDir, 100)
	if captureErr == nil {
		t.Log("CapturePane unexpectedly succeeded (tmux not running in test env); skipping capture failure assertion")
	}

	// Step 2: SendIdleReminder succeeds regardless of capture failure.
	cfg := config.DefaultConfig()
	cfg.UINode = "messenger"
	err := SendIdleReminder(cfg, activity.PaneID, tmpDir, "test-context", "messenger", activity)
	if err != nil {
		t.Fatalf("SendIdleReminder failed after capture failure: %v", err)
	}

	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("reading post dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 reminder post file despite capture failure, got %d", len(entries))
	}
}

func TestReminderState_CooldownIsIndependentOfCapture(t *testing.T) {
	// ReminderState tracks only reminder sends, not captures.
	// Capture state has no influence on ShouldSendReminder.
	rs := NewReminderState()

	// No reminder sent yet — should send.
	if !rs.ShouldSendReminder("%1", 60.0) {
		t.Error("expected ShouldSendReminder to return true on first call")
	}

	// Mark sent.
	rs.MarkReminderSent("%1")

	// Within cooldown — should not send.
	if rs.ShouldSendReminder("%1", 60.0) {
		t.Error("expected ShouldSendReminder to return false within cooldown")
	}

	// Different pane — unaffected.
	if !rs.ShouldSendReminder("%2", 60.0) {
		t.Error("expected ShouldSendReminder to return true for different pane")
	}
}
