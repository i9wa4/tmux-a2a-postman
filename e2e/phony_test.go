package e2e_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

// writeBndToml writes a bindings.toml to tmpDir and returns its path.
// mode is applied after writing (e.g. 0o600).
func writeBndToml(t *testing.T, dir, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, "bindings.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeBndToml: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("writeBndToml chmod: %v", err)
	}
	return path
}

// activeBinding is a minimal bindings.toml with one active Row 3 entry.
const activeBinding = `
[[binding]]
channel_id        = "ch-e2e"
node_name         = "ext-worker"
context_id        = "ctx-e2e"
session_name      = "test-session"
pane_title        = "ext-pane"
pane_node_name    = ""
active            = true
permitted_senders = ["orchestrator"]
`

// TestPhonyDelivery_Success verifies that DeliverToPhonyNode writes a file
// to the phony inbox when active=true and sender is permitted.
// The dead-letter directory must NOT exist after a clean delivery.
// Loads from a real bindings.toml file (not an in-memory registry) to add
// value beyond the unit tests in internal/message.
func TestPhonyDelivery_Success(t *testing.T) {
	baseDir := t.TempDir()
	cfgDir := t.TempDir()
	bndPath := writeBndToml(t, cfgDir, activeBinding, 0o600)

	reg, err := binding.Load(bndPath)
	if err != nil {
		t.Fatalf("binding.Load: %v", err)
	}

	msg := message.Message{Body: "hello from orchestrator"}
	if err := message.DeliverToPhonyNode(baseDir, "ctx-e2e", "ext-worker", "orchestrator", reg, msg); err != nil {
		t.Fatalf("DeliverToPhonyNode: %v", err)
	}

	// Assert: inbox file was created.
	inboxDir := filepath.Join(baseDir, "ctx-e2e", "phony", "ext-worker", "inbox")
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("reading inbox dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("inbox is empty after delivery")
	}

	// Assert: dead-letter dir does NOT exist (writePhonyDeadLetter only runs on failure).
	dlDir := filepath.Join(baseDir, "ctx-e2e", "phony", "ext-worker", "dead-letter")
	if _, err := os.Stat(dlDir); !os.IsNotExist(err) {
		t.Errorf("dead-letter dir should not exist after clean delivery; got err=%v", err)
	}
}

// inactiveBinding is an assigned but inactive (Row 6) entry.
const inactiveBinding = `
[[binding]]
channel_id        = "ch-inactive"
node_name         = "ext-worker"
context_id        = "ctx-e2e"
session_name      = "test-session"
pane_title        = "ext-pane"
pane_node_name    = ""
active            = false
permitted_senders = ["orchestrator"]
`

// TestPhonyDelivery_ChannelUnbound verifies that inactive bindings produce
// a dead-letter file with reason channel_unbound.
func TestPhonyDelivery_ChannelUnbound(t *testing.T) {
	baseDir := t.TempDir()
	cfgDir := t.TempDir()
	bndPath := writeBndToml(t, cfgDir, inactiveBinding, 0o600)

	reg, err := binding.Load(bndPath)
	if err != nil {
		t.Fatalf("binding.Load: %v", err)
	}

	msg := message.Message{Body: "should be dead-lettered"}
	if err := message.DeliverToPhonyNode(baseDir, "ctx-e2e", "ext-worker", "orchestrator", reg, msg); err != nil {
		t.Fatalf("DeliverToPhonyNode (channel_unbound): %v", err)
	}

	dlDir := filepath.Join(baseDir, "ctx-e2e", "phony", "ext-worker", "dead-letter")
	entries, err := os.ReadDir(dlDir)
	if err != nil {
		t.Fatalf("reading dead-letter dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("dead-letter dir is empty; expected channel_unbound dead-letter file")
	}
}

// TestPhonyDelivery_RoutingDenied verifies that an unpermitted sender produces
// a dead-letter file with reason routing_denied.
func TestPhonyDelivery_RoutingDenied(t *testing.T) {
	baseDir := t.TempDir()
	cfgDir := t.TempDir()
	bndPath := writeBndToml(t, cfgDir, activeBinding, 0o600)

	reg, err := binding.Load(bndPath)
	if err != nil {
		t.Fatalf("binding.Load: %v", err)
	}

	msg := message.Message{Body: "should be denied"}
	if err := message.DeliverToPhonyNode(baseDir, "ctx-e2e", "ext-worker", "intruder", reg, msg); err != nil {
		t.Fatalf("DeliverToPhonyNode (routing_denied): %v", err)
	}

	dlDir := filepath.Join(baseDir, "ctx-e2e", "phony", "ext-worker", "dead-letter")
	entries, err := os.ReadDir(dlDir)
	if err != nil {
		t.Fatalf("reading dead-letter dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("dead-letter dir is empty; expected routing_denied dead-letter file")
	}
}
