package ping

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestSendPingToNode_InboxPath(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	nodeInfo := discovery.NodeInfo{
		PaneID:      "%100",
		SessionName: "test-session",
		SessionDir:  sessionDir,
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}

	// Template includes {inbox_path} to verify it is expanded
	tmpl := "node: {node}\ninbox: {inbox_path}"
	if err := SendPingToNode(nodeInfo, "test-ctx", "worker", tmpl, cfg, []string{"worker"}, map[string]bool{}, map[string][]string{}, map[string]discovery.NodeInfo{}); err != nil {
		t.Fatalf("SendPingToNode() error = %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(sessionDir, "post"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}

	content, err := os.ReadFile(filepath.Join(sessionDir, "post", entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := string(content)

	// {inbox_path} must be expanded (not literal)
	if strings.Contains(body, "{inbox_path}") {
		t.Errorf("inbox_path was not expanded; body contains literal {inbox_path}: %q", body)
	}
	// Expanded value must contain the expected path components
	expectedInbox := filepath.Join(sessionDir, "inbox", "worker")
	if !strings.Contains(body, expectedInbox) {
		t.Errorf("expected inbox path %q in body, got: %q", expectedInbox, body)
	}
}

func TestSendPingToNode_SentinelObfuscation(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	nodeInfo := discovery.NodeInfo{
		PaneID:      "%100",
		SessionName: "test-session",
		SessionDir:  sessionDir,
	}

	// Node template (user-configured) contains the end-of-message sentinel.
	nodeTemplate := "# WORKER\n<!-- end of message -->\nSome content"

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"worker": {Template: nodeTemplate},
		},
	}

	// Ping template wraps with both protocol sentinels.
	tmpl := "<!-- message start -->\n{template}\n<!-- end of message -->\n"

	if err := SendPingToNode(nodeInfo, "test-ctx", "worker", tmpl, cfg, []string{"worker"}, map[string]bool{}, map[string][]string{}, map[string]discovery.NodeInfo{}); err != nil {
		t.Fatalf("SendPingToNode() error = %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(sessionDir, "post"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}

	content, err := os.ReadFile(filepath.Join(sessionDir, "post", entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := string(content)

	// User content sentinel must be obfuscated.
	if strings.Contains(body, "# WORKER\n<!-- end of message -->") {
		t.Errorf("user template sentinel was not obfuscated; body: %q", body)
	}
	if !strings.Contains(body, "<!-- end of msg -->") {
		t.Errorf("expected obfuscated sentinel in body; got: %q", body)
	}
	// Protocol wrapper sentinel must remain intact at the end.
	if !strings.HasSuffix(strings.TrimRight(body, "\n"), "<!-- end of message -->") {
		t.Errorf("protocol sentinel was altered or missing; body: %q", body)
	}
}

func TestSendPingToNode(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	nodeInfo := discovery.NodeInfo{
		PaneID:      "%100",
		SessionName: "test-session",
		SessionDir:  sessionDir,
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}

	activeNodes := []string{"worker", "orchestrator"}
	pongActiveNodes := map[string]bool{} // Empty for this test (PING time)
	err := SendPingToNode(nodeInfo, "test-ctx", "worker", "PING {node} in {context_id}", cfg, activeNodes, pongActiveNodes, map[string][]string{}, map[string]discovery.NodeInfo{})
	if err != nil {
		t.Fatalf("SendPingToNode() error = %v", err)
	}

	// Verify post directory was created
	postDir := filepath.Join(sessionDir, "post")
	if _, err := os.Stat(postDir); os.IsNotExist(err) {
		t.Errorf("post directory was not created")
	}

	// Verify message file was created
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}

	// Verify filename format
	filename := entries[0].Name()
	if !strings.HasSuffix(filename, "-from-postman-to-worker.md") {
		t.Errorf("filename = %q, want suffix '-from-postman-to-worker.md'", filename)
	}

	// Verify content
	content, err := os.ReadFile(filepath.Join(postDir, filename))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), "PING worker in test-ctx") {
		t.Errorf("content = %q, want to contain 'PING worker in test-ctx'", string(content))
	}
}
