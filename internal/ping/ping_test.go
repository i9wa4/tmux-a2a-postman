package ping

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestBuildPingMessage(t *testing.T) {
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		timeout  time.Duration
		want     string
	}{
		{
			name:     "basic variable expansion",
			template: "PING {node} in {context_id}",
			vars: map[string]string{
				"node":       "worker",
				"context_id": "test-ctx",
			},
			timeout: 5 * time.Second,
			want:    "PING worker in test-ctx",
		},
		{
			name:     "no variables",
			template: "PING message",
			vars:     map[string]string{},
			timeout:  5 * time.Second,
			want:     "PING message",
		},
		{
			name:     "missing variable",
			template: "PING {node} in {missing}",
			vars: map[string]string{
				"node": "worker",
			},
			timeout: 5 * time.Second,
			want:    "PING worker in {missing}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildPingMessage(tt.template, tt.vars, tt.timeout)
			if got != tt.want {
				t.Errorf("BuildPingMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSendPingToNode_MaterializedPath(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	matPath := "/fake/path/to/worker.md"

	nodeInfo := discovery.NodeInfo{
		PaneID:      "%100",
		SessionName: "test-session",
		SessionDir:  sessionDir,
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		MaterializedPaths: map[string]string{
			"worker": matPath,
		},
	}

	// Use a template with {template} at the end to simulate the vulnerable case
	tmpl := "header line\n{template}"
	if err := SendPingToNode(nodeInfo, "test-ctx", "worker", tmpl, cfg, []string{"worker"}, map[string]bool{}); err != nil {
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

	// @path\n must appear somewhere in the body
	if !strings.Contains(body, "@"+matPath+"\n") {
		t.Errorf("expected @path\\n in body, got: %q", body)
	}
	// Body must NOT end with bare @path (shell autocomplete guard)
	if strings.HasSuffix(body, "@"+matPath) {
		t.Errorf("body must not end with bare @path (no trailing newline): %q", body)
	}
}

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
	if err := SendPingToNode(nodeInfo, "test-ctx", "worker", tmpl, cfg, []string{"worker"}, map[string]bool{}); err != nil {
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

	if err := SendPingToNode(nodeInfo, "test-ctx", "worker", tmpl, cfg, []string{"worker"}, map[string]bool{}); err != nil {
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
	err := SendPingToNode(nodeInfo, "test-ctx", "worker", "PING {node} in {context_id}", cfg, activeNodes, pongActiveNodes)
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

func TestSendPingToNode_WrapperPresent(t *testing.T) {
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

	// Template missing both protocol wrapper markers — warning should fire.
	tmpl := "plain text without markers"

	// Redirect stderr to capture warning output.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	if sendErr := SendPingToNode(nodeInfo, "test-ctx", "worker", tmpl, cfg, []string{"worker"}, map[string]bool{}); sendErr != nil {
		w.Close()
		os.Stderr = origStderr
		t.Fatalf("SendPingToNode() error = %v", sendErr)
	}

	w.Close()
	os.Stderr = origStderr

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if !strings.Contains(buf.String(), "missing protocol wrapper markers") {
		t.Errorf("expected missing-markers warning, stderr: %q", buf.String())
	}
}
