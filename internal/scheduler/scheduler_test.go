package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "schedules.toml")
	content := `
[[schedules]]
cron = "0 7 * * *"
to = "orchestrator"
message = "Run /generate-trends"

[[schedules]]
cron = "0 23 * * *"
to = "orchestrator"
message = "Run /vault-digest"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Schedules) != 2 {
		t.Fatalf("Schedules length: got %d, want 2", len(cfg.Schedules))
	}
	if cfg.Schedules[0].Cron != "0 7 * * *" {
		t.Errorf("Schedules[0].Cron: got %q, want %q", cfg.Schedules[0].Cron, "0 7 * * *")
	}
	if cfg.Schedules[1].To != "orchestrator" {
		t.Errorf("Schedules[1].To: got %q, want %q", cfg.Schedules[1].To, "orchestrator")
	}
}

func TestParseCron(t *testing.T) {
	// "0 7 * * *" = every day at 07:00
	ref := time.Date(2026, 3, 10, 6, 30, 0, 0, time.UTC)
	next, err := ParseCron("0 7 * * *", ref)
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	want := time.Date(2026, 3, 10, 7, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("ParseCron: got %v, want %v", next, want)
	}

	// After 07:00, next should be tomorrow
	ref2 := time.Date(2026, 3, 10, 7, 30, 0, 0, time.UTC)
	next2, err := ParseCron("0 7 * * *", ref2)
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	want2 := time.Date(2026, 3, 11, 7, 0, 0, 0, time.UTC)
	if !next2.Equal(want2) {
		t.Errorf("ParseCron after: got %v, want %v", next2, want2)
	}
}

func TestParseCron_Weekly(t *testing.T) {
	// "0 9 * * 1" = every Monday at 09:00
	// 2026-03-10 is a Tuesday
	ref := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	next, err := ParseCron("0 9 * * 1", ref)
	if err != nil {
		t.Fatalf("ParseCron weekly: %v", err)
	}
	// Next Monday is 2026-03-16
	want := time.Date(2026, 3, 16, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("ParseCron weekly: got %v, want %v", next, want)
	}
}

func TestParseCron_Invalid(t *testing.T) {
	_, err := ParseCron("invalid", time.Now())
	if err == nil {
		t.Fatal("expected error for invalid cron, got nil")
	}
}

func TestStateKey(t *testing.T) {
	s := Schedule{Cron: "0 7 * * *", To: "orchestrator"}
	key := StateKey(s)
	if key != "0 7 * * *:orchestrator" {
		t.Errorf("StateKey: got %q, want %q", key, "0 7 * * *:orchestrator")
	}
}

func TestLoadState_Missing(t *testing.T) {
	state := LoadState("/nonexistent/path.json")
	if state == nil {
		t.Fatal("LoadState returned nil")
	}
	if len(state.Schedules) != 0 {
		t.Errorf("LoadState empty: got %d entries, want 0", len(state.Schedules))
	}
}

func TestSaveAndLoadState(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.json")

	now := time.Now().Truncate(time.Second)
	state := &State{
		Schedules: map[string]StateEntry{
			"0 7 * * *:orchestrator": {LastFiredAt: now},
		},
	}
	if err := SaveState(path, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded := LoadState(path)
	entry, exists := loaded.Schedules["0 7 * * *:orchestrator"]
	if !exists {
		t.Fatal("entry not found after load")
	}
	if !entry.LastFiredAt.Equal(now) {
		t.Errorf("LastFiredAt: got %v, want %v", entry.LastFiredAt, now)
	}

	// Verify atomic write (no .tmp left behind)
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error(".tmp file should not exist after successful save")
	}
}

func TestLoadState_Corrupt(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	state := LoadState(path)
	if state == nil {
		t.Fatal("LoadState returned nil for corrupt file")
	}
	if len(state.Schedules) != 0 {
		t.Errorf("LoadState corrupt: got %d entries, want 0", len(state.Schedules))
	}
}

func TestDraftPath(t *testing.T) {
	path := DraftPath("/base", "ctx-1", "session-a", "orchestrator")
	if !filepath.IsAbs(path) {
		t.Errorf("DraftPath should be absolute: got %q", path)
	}
	if filepath.Dir(path) != "/base/ctx-1/session-a/draft" {
		t.Errorf("DraftPath dir: got %q", filepath.Dir(path))
	}
}

func TestMatchField(t *testing.T) {
	if !matchField("*", 5) {
		t.Error("wildcard should match any value")
	}
	if !matchField("7", 7) {
		t.Error("exact match should pass")
	}
	if matchField("7", 8) {
		t.Error("non-matching should fail")
	}
	if !matchField("1,3,5", 3) {
		t.Error("comma-separated should match")
	}
	if matchField("1,3,5", 4) {
		t.Error("comma-separated non-match should fail")
	}
}

func TestSaveState_JSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.json")
	now := time.Now().Truncate(time.Second)
	state := &State{
		Schedules: map[string]StateEntry{
			"0 7 * * *:orchestrator": {LastFiredAt: now},
		},
	}
	if err := SaveState(path, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Verify the file is valid JSON
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Errorf("SaveState wrote invalid JSON: %v", err)
	}
}
