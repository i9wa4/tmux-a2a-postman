package ping

import (
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
	err := SendPingToNode(nodeInfo, "test-ctx", "worker", "PING {node} in {context_id}", cfg, activeNodes)
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
