package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBackfillVerdictEventsOutputsJSONL(t *testing.T) {
	sessionDir := t.TempDir()
	readDir := filepath.Join(sessionDir, "read")
	if err := os.MkdirAll(readDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(read): %v", err)
	}
	content := "---\nparams:\n" +
		"  from: worker\n" +
		"  to: orchestrator\n" +
		"  messageId: m1.md\n" +
		"  timestamp: 2026-07-13T10:00:00Z\n" +
		"---\n\nDONE: complete\n"
	if err := os.WriteFile(filepath.Join(readDir, "m1.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(archive): %v", err)
	}

	var stdout bytes.Buffer
	if err := runBackfillVerdictEvents(&stdout, []string{"--session-dir", sessionDir}); err != nil {
		t.Fatalf("runBackfillVerdictEvents() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1: %q", len(lines), stdout.String())
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("json.Unmarshal(row): %v", err)
	}
	if row["event_type"] != "verdict_event" {
		t.Fatalf("event_type = %#v, want verdict_event", row["event_type"])
	}
	if row["source"] != "backfill" {
		t.Fatalf("source = %#v, want backfill", row["source"])
	}
	if row["marker"] != "DONE" {
		t.Fatalf("marker = %#v, want DONE", row["marker"])
	}
}
