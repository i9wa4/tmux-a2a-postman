package diplomat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockRenamer records rename calls and optionally returns an error.
type mockRenamer struct {
	calls []struct{ src, dst string }
	err   error
}

func (m *mockRenamer) Rename(src, dst string) error {
	m.calls = append(m.calls, struct{ src, dst string }{src, dst})
	if m.err != nil {
		return m.err
	}
	return os.Rename(src, dst)
}

func setupDeliveryTest(t *testing.T) (baseDir, contextID, sessionName, postPath string) {
	t.Helper()
	baseDir = t.TempDir()
	contextID = "ctx-1"
	sessionName = "session-a"

	// Create diplomat drop dir and a test file
	dropDir := filepath.Join(baseDir, "diplomat", contextID, "post")
	if err := os.MkdirAll(dropDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	postPath = filepath.Join(dropDir, "test-msg.md")
	if err := os.WriteFile(postPath, []byte("test message"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return
}

func TestDeliverCrossContextMessage_Success(t *testing.T) {
	baseDir, contextID, sessionName, postPath := setupDeliveryTest(t)
	d := NewDeliverer()
	r := &mockRenamer{}

	reason, err := d.DeliverCrossContextMessage(
		postPath, baseDir, contextID, sessionName,
		"worker", "trace-1", 0, r,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != "" {
		t.Errorf("reason: got %q, want empty", reason)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 rename call, got %d", len(r.calls))
	}
	if !strings.Contains(r.calls[0].dst, "inbox/worker") {
		t.Errorf("destination should contain inbox/worker: %q", r.calls[0].dst)
	}
}

func TestDeliverCrossContextMessage_HopLimit(t *testing.T) {
	baseDir, contextID, sessionName, postPath := setupDeliveryTest(t)
	d := NewDeliverer()
	r := &mockRenamer{}

	reason, _ := d.DeliverCrossContextMessage(
		postPath, baseDir, contextID, sessionName,
		"worker", "trace-1", 1, r,
	)
	if reason != "hop_limit" {
		t.Errorf("reason: got %q, want %q", reason, "hop_limit")
	}
}

func TestDeliverCrossContextMessage_MissingTarget(t *testing.T) {
	baseDir, contextID, sessionName, postPath := setupDeliveryTest(t)
	d := NewDeliverer()
	r := &mockRenamer{}

	reason, _ := d.DeliverCrossContextMessage(
		postPath, baseDir, contextID, sessionName,
		"", "trace-2", 0, r,
	)
	if reason != "missing_target_node" {
		t.Errorf("reason: got %q, want %q", reason, "missing_target_node")
	}
}

func TestDeliverCrossContextMessage_DuplicateTraceID(t *testing.T) {
	baseDir, contextID, sessionName, postPath := setupDeliveryTest(t)
	d := NewDeliverer()
	r := &mockRenamer{}

	// First delivery succeeds
	reason, _ := d.DeliverCrossContextMessage(
		postPath, baseDir, contextID, sessionName,
		"worker", "trace-dup", 0, r,
	)
	if reason != "" {
		t.Fatalf("first delivery should succeed: reason=%q", reason)
	}

	// Create another file for second delivery
	dropDir := filepath.Join(baseDir, "diplomat", contextID, "post")
	postPath2 := filepath.Join(dropDir, "test-msg-2.md")
	if err := os.WriteFile(postPath2, []byte("test message 2"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Second delivery with same trace-id should be dead-lettered
	reason2, _ := d.DeliverCrossContextMessage(
		postPath2, baseDir, contextID, sessionName,
		"worker", "trace-dup", 0, r,
	)
	if reason2 != "duplicate_trace_id" {
		t.Errorf("reason: got %q, want %q", reason2, "duplicate_trace_id")
	}
}

func TestDeliverCrossContextMessage_RenameFailure(t *testing.T) {
	baseDir, contextID, sessionName, postPath := setupDeliveryTest(t)
	d := NewDeliverer()
	r := &mockRenamer{err: fmt.Errorf("disk full")}

	reason, err := d.DeliverCrossContextMessage(
		postPath, baseDir, contextID, sessionName,
		"worker", "trace-3", 0, r,
	)
	if reason != "rename_failed" {
		t.Errorf("reason: got %q, want %q", reason, "rename_failed")
	}
	if err == nil {
		t.Error("expected error for rename failure")
	}
}

func TestGenerateTraceID(t *testing.T) {
	id, err := GenerateTraceID()
	if err != nil {
		t.Fatalf("GenerateTraceID: %v", err)
	}
	// UUID4 format: 8-4-4-4-12 hex chars
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Errorf("expected 5 parts, got %d: %q", len(parts), id)
	}
	// Version should be 4
	if len(parts) >= 3 && parts[2][0] != '4' {
		t.Errorf("UUID version should be 4: got %q", parts[2])
	}
}

func TestParseCrossContextTarget(t *testing.T) {
	ctx, node, err := ParseCrossContextTarget("session-abc:worker")
	if err != nil {
		t.Fatalf("ParseCrossContextTarget: %v", err)
	}
	if ctx != "session-abc" {
		t.Errorf("contextID: got %q, want %q", ctx, "session-abc")
	}
	if node != "worker" {
		t.Errorf("node: got %q, want %q", node, "worker")
	}
}

func TestParseCrossContextTarget_Invalid(t *testing.T) {
	tests := []string{"", "nocolon", ":node", "ctx:", ":"}
	for _, input := range tests {
		_, _, err := ParseCrossContextTarget(input)
		if err == nil {
			t.Errorf("ParseCrossContextTarget(%q) should fail", input)
		}
	}
}

func TestCrossContextParsing(t *testing.T) {
	contextID, node, err := ParseCrossContextTarget("ctx-abc:worker")
	if err != nil {
		t.Fatalf("ParseCrossContextTarget: %v", err)
	}
	if contextID != "ctx-abc" {
		t.Errorf("contextID = %q, want %q", contextID, "ctx-abc")
	}
	if node != "worker" {
		t.Errorf("node = %q, want %q", node, "worker")
	}
}

func TestCrossContextTraceIDFormat(t *testing.T) {
	id, err := GenerateTraceID()
	if err != nil {
		t.Fatalf("GenerateTraceID: %v", err)
	}
	// Version 4 UUID: 8-4-4-4-12 hex chars, version nibble = 4
	parts := strings.SplitN(id, "-", 5)
	if len(parts) != 5 {
		t.Fatalf("trace ID has %d parts, want 5: %q", len(parts), id)
	}
	if len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		t.Errorf("trace ID part lengths wrong: %q", id)
	}
	if parts[2][0] != '4' {
		t.Errorf("trace ID version nibble = %q, want '4': %q", string(parts[2][0]), id)
	}
}
