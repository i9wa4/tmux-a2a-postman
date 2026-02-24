package watchdog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSendLivenessPing(t *testing.T) {
	tmpDir := t.TempDir()
	postDir := filepath.Join(tmpDir, "post")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		t.Fatalf("MkdirAll post dir: %v", err)
	}

	contextID := "test-context-abc"
	uiNode := "orchestrator"

	if err := SendLivenessPing(tmpDir, contextID, uiNode); err != nil {
		t.Fatalf("SendLivenessPing: %v", err)
	}

	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in post/, got %d", len(entries))
	}

	content, err := os.ReadFile(filepath.Join(postDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := string(content)

	if !strings.Contains(body, "contextId: "+contextID) {
		t.Errorf("missing contextId in message body")
	}
	if !strings.Contains(body, "from: watchdog") {
		t.Errorf("missing 'from: watchdog' in message body")
	}
	if !strings.Contains(body, "to: "+uiNode) {
		t.Errorf("missing 'to: %s' in message body", uiNode)
	}
	if !strings.Contains(body, "## Heartbeat") {
		t.Errorf("missing '## Heartbeat' section in message body")
	}
	if !strings.Contains(body, "Watchdog is alive and monitoring.") {
		t.Errorf("missing 'Watchdog is alive and monitoring.' in message body")
	}
}

func TestStartLivenessPing_StopsCleanly(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "post"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Use a very short interval so the ticker fires quickly
	stopChan := StartLivenessPing(tmpDir, "ctx-test", "orchestrator", 0.05)

	// Let the goroutine run briefly (at least one tick), then close the stop channel
	time.Sleep(80 * time.Millisecond)
	close(stopChan)

	// Goroutine stops cleanly — wait briefly to confirm no panic or hang
	time.Sleep(20 * time.Millisecond)

	// Verify at least one file was written during the run
	entries, err := os.ReadDir(filepath.Join(tmpDir, "post"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("expected at least one liveness ping file to be written")
	}
}

func TestStartLivenessPing_DisabledWhenZeroInterval(t *testing.T) {
	tmpDir := t.TempDir()
	postDir := filepath.Join(tmpDir, "post")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Zero interval means disabled — goroutine returns immediately
	stopChan := StartLivenessPing(tmpDir, "ctx-test", "orchestrator", 0)
	close(stopChan)

	time.Sleep(20 * time.Millisecond)

	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 files when interval=0 (disabled), got %d", len(entries))
	}
}
