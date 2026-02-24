package heartbeat

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// makeSharedNodes builds an atomic.Pointer wrapping a single-node map.
func makeSharedNodes(llmNode, sessionDir string) *atomic.Pointer[map[string]discovery.NodeInfo] {
	nodes := map[string]discovery.NodeInfo{
		llmNode: {SessionDir: sessionDir},
	}
	var ptr atomic.Pointer[map[string]discovery.NodeInfo]
	ptr.Store(&nodes)
	return &ptr
}

func TestSendHeartbeatTrigger_WritesFile(t *testing.T) {
	tmpDir := t.TempDir()
	postDir := filepath.Join(tmpDir, "post")
	inboxDir := filepath.Join(tmpDir, "inbox", "heartbeat-llm")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		t.Fatalf("MkdirAll post: %v", err)
	}
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}

	sharedNodes := makeSharedNodes("heartbeat-llm", tmpDir)

	if err := SendHeartbeatTrigger(sharedNodes, "ctx-test", "heartbeat-llm", "check {context_id}", 1800); err != nil {
		t.Fatalf("SendHeartbeatTrigger: %v", err)
	}

	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 trigger file in post/, got %d", len(entries))
	}

	content, err := os.ReadFile(filepath.Join(postDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := string(content)
	if body == "" {
		t.Error("trigger file content should not be empty")
	}
	// {context_id} should be substituted
	if !strings.Contains(body, "ctx-test") {
		t.Errorf("expected context_id substitution in body, got: %q", body)
	}
}

func TestSendHeartbeatTrigger_SkipsWhenUnread(t *testing.T) {
	tmpDir := t.TempDir()
	postDir := filepath.Join(tmpDir, "post")
	inboxDir := filepath.Join(tmpDir, "inbox", "heartbeat-llm")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		t.Fatalf("MkdirAll post: %v", err)
	}
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}

	// Place a fresh (within TTL) file in the inbox
	freshFile := filepath.Join(inboxDir, "20240101-120000-from-postman-to-heartbeat-llm.md")
	if err := os.WriteFile(freshFile, []byte("trigger"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sharedNodes := makeSharedNodes("heartbeat-llm", tmpDir)

	// intervalSeconds = 1800 → TTL = 3600s; the fresh file is well within TTL
	if err := SendHeartbeatTrigger(sharedNodes, "ctx-test", "heartbeat-llm", "prompt", 1800); err != nil {
		t.Fatalf("SendHeartbeatTrigger: %v", err)
	}

	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 files in post/ (skipped while unread), got %d", len(entries))
	}
}

func TestSendHeartbeatTrigger_RecyclesStale(t *testing.T) {
	tmpDir := t.TempDir()
	postDir := filepath.Join(tmpDir, "post")
	inboxDir := filepath.Join(tmpDir, "inbox", "heartbeat-llm")
	deadLetterDir := filepath.Join(tmpDir, "dead-letter")
	for _, dir := range []string{postDir, inboxDir, deadLetterDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}

	// Place a stale file in the inbox (mtime set to past)
	staleFile := filepath.Join(inboxDir, "20240101-000000-from-postman-to-heartbeat-llm.md")
	if err := os.WriteFile(staleFile, []byte("old trigger"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Set mtime to 2 hours ago so it exceeds TTL of 2×1s = 2s
	pastTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(staleFile, pastTime, pastTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	sharedNodes := makeSharedNodes("heartbeat-llm", tmpDir)

	// intervalSeconds = 1 → TTL = 2s; 2-hour-old file is stale
	if err := SendHeartbeatTrigger(sharedNodes, "ctx-test", "heartbeat-llm", "prompt", 1); err != nil {
		t.Fatalf("SendHeartbeatTrigger: %v", err)
	}

	// Stale file moved to dead-letter/
	deadEntries, err := os.ReadDir(deadLetterDir)
	if err != nil {
		t.Fatalf("ReadDir dead-letter: %v", err)
	}
	if len(deadEntries) != 1 {
		t.Errorf("expected 1 file in dead-letter/, got %d", len(deadEntries))
	}

	// New trigger written to post/
	postEntries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(postEntries) != 1 {
		t.Errorf("expected 1 trigger file in post/ after recycling stale, got %d", len(postEntries))
	}
}

func TestSendHeartbeatTrigger_NodeNotFound(t *testing.T) {
	var ptr atomic.Pointer[map[string]discovery.NodeInfo]
	emptyNodes := map[string]discovery.NodeInfo{}
	ptr.Store(&emptyNodes)

	// Node not in sharedNodes: should return nil and write nothing
	if err := SendHeartbeatTrigger(&ptr, "ctx-test", "heartbeat-llm", "prompt", 1800); err != nil {
		t.Errorf("expected nil error for missing node, got: %v", err)
	}
}
