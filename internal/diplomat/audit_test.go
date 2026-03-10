package diplomat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAuditLog(t *testing.T) {
	baseDir := t.TempDir()

	entry1 := `{"timestamp":"2026-03-10T07:00:00Z","outcome":"delivered"}`
	entry2 := `{"timestamp":"2026-03-10T08:00:00Z","outcome":"dead_letter"}`

	if err := AppendAuditLog(baseDir, entry1); err != nil {
		t.Fatalf("AppendAuditLog 1: %v", err)
	}
	if err := AppendAuditLog(baseDir, entry2); err != nil {
		t.Fatalf("AppendAuditLog 2: %v", err)
	}

	logPath := filepath.Join(baseDir, "diplomat", "audit.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "delivered") {
		t.Errorf("line 0 should contain 'delivered': %q", lines[0])
	}
	if !strings.Contains(lines[1], "dead_letter") {
		t.Errorf("line 1 should contain 'dead_letter': %q", lines[1])
	}
}

func TestCheckAllowlist_Empty(t *testing.T) {
	if !CheckAllowlist(nil, "any-node") {
		t.Error("empty allowlist should allow all")
	}
	if !CheckAllowlist([]string{}, "any-node") {
		t.Error("empty slice allowlist should allow all")
	}
}

func TestCheckAllowlist_Allowed(t *testing.T) {
	list := []string{"diplomat-a", "diplomat-b"}
	if !CheckAllowlist(list, "diplomat-a") {
		t.Error("diplomat-a should be allowed")
	}
}

func TestCheckAllowlist_Denied(t *testing.T) {
	list := []string{"diplomat-a", "diplomat-b"}
	if CheckAllowlist(list, "diplomat-c") {
		t.Error("diplomat-c should be denied")
	}
}
