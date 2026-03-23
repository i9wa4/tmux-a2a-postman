package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

// agentSessionHarness provides a two-node session for testing cross-session
// inbox delivery behaviors: queue depth cap, dead-letter, and sender allowlist.
// Neutral naming: no "uma", "vault", or "uma-in-vault" identifiers.
//
// Node layout:
//
//	project-session:sender-node  → caller sending to the persistent-agent
//	project-session:agent-node   → persistent-agent inbox receiver
//
// knownNodes uses session-prefixed keys; adjacency entries use session:node form
// for the agent-node to verify that session:node syntax in adjacency is honored.
type agentSessionHarness struct {
	baseDir    string
	contextID  string
	sessionDir string
	nodes      map[string]discovery.NodeInfo
	adjacency  map[string][]string
	cfg        *config.Config
}

const (
	harnessSession    = "project-session"
	harnessSenderNode = "sender-node"
	harnessAgentNode  = "agent-node"
	harnessContextID  = "test-ctx"
)

// newAgentSessionHarness sets up session directories and returns a ready harness.
// Uses t.TempDir() for automatic cleanup.
func newAgentSessionHarness(t *testing.T) *agentSessionHarness {
	t.Helper()
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, harnessContextID, harnessSession)

	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("newAgentSessionHarness: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		harnessSession + ":" + harnessSenderNode: {
			PaneID:      "pane-sender",
			SessionName: harnessSession,
			SessionDir:  sessionDir,
		},
		harnessSession + ":" + harnessAgentNode: {
			PaneID:      "pane-agent",
			SessionName: harnessSession,
			SessionDir:  sessionDir,
		},
	}

	// sender-node is permitted to reach agent-node.
	// adjacency values use session:node form to verify that syntax is honored.
	adjacency := map[string][]string{
		harnessSenderNode: {harnessSession + ":" + harnessAgentNode},
		harnessAgentNode:  {harnessSession + ":" + harnessSenderNode},
	}

	cfg := &config.Config{
		EnterDelay:  0,
		TmuxTimeout: 0.1,
	}

	return &agentSessionHarness{
		baseDir:    baseDir,
		contextID:  harnessContextID,
		sessionDir: sessionDir,
		nodes:      nodes,
		adjacency:  adjacency,
		cfg:        cfg,
	}
}

// postAndDeliver writes a message to post/ with a sequence-unique filename
// and calls DeliverMessage synchronously. seq must be unique within a test.
func (h *agentSessionHarness) postAndDeliver(t *testing.T, from, to string, seq int) {
	t.Helper()
	ts := fmt.Sprintf("20260101-%06d", seq)
	filename := ts + "-from-" + from + "-to-" + to + ".md"
	content := fmt.Sprintf(
		"---\nmethod: message/send\nparams:\n  contextId: %s\n  from: %s\n  to: %s\n  timestamp: %s\n---\n\ntest body %d\n",
		h.contextID, from, to,
		time.Now().Format("2006-01-02T15:04:05"),
		seq,
	)
	postPath := filepath.Join(h.sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("postAndDeliver(seq=%d): writing post: %v", seq, err)
	}
	if err := message.DeliverMessage(
		postPath, h.contextID, h.nodes, nil, h.adjacency, h.cfg,
		func(string) bool { return true },
		nil,
		idle.NewIdleTracker(),
		"",
	); err != nil {
		t.Fatalf("postAndDeliver(seq=%d): DeliverMessage: %v", seq, err)
	}
}

// inboxCount returns the number of files in the specified node's inbox.
func (h *agentSessionHarness) inboxCount(t *testing.T, nodeName string) int {
	t.Helper()
	dir := filepath.Join(h.sessionDir, "inbox", nodeName)
	return countFiles(t, dir)
}

// deadLetterCount returns the number of files in the session dead-letter directory.
func (h *agentSessionHarness) deadLetterCount(t *testing.T) int {
	t.Helper()
	dir := filepath.Join(h.sessionDir, "dead-letter")
	return countFiles(t, dir)
}

// countFiles counts non-directory entries in dir; returns 0 if dir does not exist.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("countFiles(%q): %v", dir, err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}
