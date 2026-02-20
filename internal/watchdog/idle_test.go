package watchdog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Issue #123: Tests for GetIdlePanesFromActivityFile

func TestGetIdlePanesFromActivityFile_IdlePanes(t *testing.T) {
	// New format: map[string]paneActivityExport — only "idle" panes returned.
	now := time.Now()
	data := map[string]paneActivityExport{
		"%10": {Status: "active", LastChangeAt: now.Add(-10 * time.Second)},
		"%11": {Status: "idle", LastChangeAt: now.Add(-500 * time.Second)},
		"%12": {Status: "stale", LastChangeAt: now.Add(-1000 * time.Second)},
	}
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshaling test data: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pane-activity.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	result, err := GetIdlePanesFromActivityFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 idle pane, got %d: %v", len(result), result)
	}
	if result[0].PaneID != "%11" {
		t.Errorf("expected pane %%11, got %q", result[0].PaneID)
	}
	if result[0].LastActivityTime.IsZero() {
		t.Errorf("expected LastActivityTime to be set for %%11")
	}
}

func TestGetIdlePanesFromActivityFile_LegacyFormat(t *testing.T) {
	// Legacy format: map[string]string — "idle" panes returned, LastActivityTime is zero.
	data := map[string]string{
		"%20": "active",
		"%21": "idle",
		"%22": "stale",
	}
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshaling test data: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pane-activity.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	result, err := GetIdlePanesFromActivityFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 idle pane, got %d: %v", len(result), result)
	}
	if result[0].PaneID != "%21" {
		t.Errorf("expected pane %%21, got %q", result[0].PaneID)
	}
}

func TestGetIdlePanesFromActivityFile_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")
	result, err := GetIdlePanesFromActivityFile(path)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty slice for missing file, got %d entries", len(result))
	}
}

func TestGetIdlePanesFromActivityFile_SchemaError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pane-activity.json")
	if err := os.WriteFile(path, []byte("not valid json {{{"), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	result, err := GetIdlePanesFromActivityFile(path)
	if err != nil {
		t.Fatalf("expected no error for malformed JSON, got: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty slice for malformed JSON, got %d entries", len(result))
	}
}
