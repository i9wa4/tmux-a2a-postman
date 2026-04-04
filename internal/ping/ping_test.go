package ping

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

func readSingleInboxMessage(t *testing.T, sessionDir, nodeName string) (string, string) {
	t.Helper()

	inboxDir := filepath.Join(sessionDir, "inbox", nodeName)
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("ReadDir inbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 inbox file, got %d", len(entries))
	}

	filename := entries[0].Name()
	content, err := os.ReadFile(filepath.Join(inboxDir, filename))
	if err != nil {
		t.Fatalf("ReadFile inbox message: %v", err)
	}
	return filename, string(content)
}

func TestSendPingToNode_InboxPath(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

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

	_, body := readSingleInboxMessage(t, sessionDir, "worker")

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
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

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

	_, body := readSingleInboxMessage(t, sessionDir, "worker")

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
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	nodeInfo := discovery.NodeInfo{
		PaneID:      "%100",
		SessionName: "test-session",
		SessionDir:  sessionDir,
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}

	activeNodes := []string{"worker", "orchestrator"}
	livenessMap := map[string]bool{} // Empty for this test (PING time)
	err := SendPingToNode(nodeInfo, "test-ctx", "worker", "PING {node} in {context_id}", cfg, activeNodes, livenessMap, map[string][]string{}, map[string]discovery.NodeInfo{})
	if err != nil {
		t.Fatalf("SendPingToNode() error = %v", err)
	}

	filename, body := readSingleInboxMessage(t, sessionDir, "worker")
	if !strings.HasSuffix(filename, "-from-postman-to-worker.md") {
		t.Errorf("filename = %q, want suffix '-from-postman-to-worker.md'", filename)
	}

	if !strings.Contains(body, "PING worker in test-ctx") {
		t.Errorf("content = %q, want to contain 'PING worker in test-ctx'", body)
	}
}

func TestSendPingToNode_ReplyCommandExpandsConcreteRecipient(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	nodeInfo := discovery.NodeInfo{
		PaneID:      "%100",
		SessionName: "test-session",
		SessionDir:  sessionDir,
	}

	cfg := &config.Config{
		TmuxTimeout:  5.0,
		ReplyCommand: "tmux-a2a-postman send-message --to <recipient> --body \"<your message>\"",
	}

	if err := SendPingToNode(nodeInfo, "ctx-ping", "worker", "Reply: {reply_command}", cfg, []string{"worker"}, map[string]bool{}, map[string][]string{}, map[string]discovery.NodeInfo{}); err != nil {
		t.Fatalf("SendPingToNode() error = %v", err)
	}

	_, body := readSingleInboxMessage(t, sessionDir, "worker")
	if strings.Contains(body, "send-message") {
		t.Fatalf("ping content still contains legacy send-message: %q", body)
	}
	if strings.Contains(body, "<recipient>") {
		t.Fatalf("ping content still contains recipient placeholder: %q", body)
	}
	if !strings.Contains(body, "send --context-id ctx-ping --to worker") {
		t.Fatalf("ping content missing concrete reply target: %q", body)
	}
}

func TestSendPingToNode_DeliveryFlow(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	nodeInfo := discovery.NodeInfo{
		PaneID:      "%100",
		SessionName: "review-session",
		SessionDir:  sessionDir,
	}
	nodes := map[string]discovery.NodeInfo{
		"review-session:worker": nodeInfo,
	}
	cfg := &config.Config{
		NotificationTemplate: "notice {from_node}->{node}",
		EnterDelay:           0,
		TmuxTimeout:          1.0,
	}

	if err := SendPingToNode(nodeInfo, "test-ctx", "worker", "PING {node} in {context_id}", cfg, []string{"worker"}, map[string]bool{"review-session:worker": true}, map[string][]string{}, nodes); err != nil {
		t.Fatalf("SendPingToNode() error = %v", err)
	}

	postDir := filepath.Join(sessionDir, "post")
	entries, err := os.ReadDir(postDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) == 1 {
		postPath := filepath.Join(postDir, entries[0].Name())
		if err := message.DeliverMessage(postPath, "test-ctx", nodes, nil, map[string][]string{}, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), "local-daemon"); err != nil {
			t.Fatalf("DeliverMessage() error = %v", err)
		}
	} else if len(entries) > 1 {
		t.Fatalf("expected at most 1 post artifact, got %d", len(entries))
	}

	filename, body := readSingleInboxMessage(t, sessionDir, "worker")
	if !strings.HasSuffix(filename, "-from-postman-to-worker.md") {
		t.Fatalf("filename = %q, want suffix '-from-postman-to-worker.md'", filename)
	}
	if !strings.Contains(body, "PING worker in test-ctx") {
		t.Fatalf("content = %q, want to contain 'PING worker in test-ctx'", body)
	}

	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	deadEntries, err := os.ReadDir(deadLetterDir)
	if err != nil {
		t.Fatalf("ReadDir dead-letter: %v", err)
	}
	if len(deadEntries) != 0 {
		t.Fatalf("expected no dead-letter entries, got %d", len(deadEntries))
	}
}

func TestSendPingToNode_NotificationAttemptedOnDelivery(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "tmux-args.txt")
	fakeTmux := filepath.Join(tmpDir, "tmux")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %q\nif [ \"$1\" = \"display-message\" ]; then\n  echo bash\nfi\n", argsFile)
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fakeTmux failed: %v", err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	sessionDir := filepath.Join(tmpDir, "notify-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	nodeInfo := discovery.NodeInfo{
		PaneID:      "%99",
		SessionName: "notify-session",
		SessionDir:  sessionDir,
	}
	nodes := map[string]discovery.NodeInfo{
		"notify-session:worker": nodeInfo,
	}
	cfg := &config.Config{
		NotificationTemplate: "notice {from_node}->{node}",
		EnterDelay:           0,
		TmuxTimeout:          1.0,
	}

	if err := SendPingToNode(nodeInfo, "test-ctx", "worker", "PING {node}", cfg, []string{"worker"}, map[string]bool{"notify-session:worker": true}, map[string][]string{}, nodes); err != nil {
		t.Fatalf("SendPingToNode() error = %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile argsFile failed: %v", err)
	}
	argsLog := string(argsData)
	for _, want := range []string{
		"display-message -t %99 -p #{pane_current_command}",
		"set-buffer",
		"paste-buffer -t %99",
		"send-keys -t %99 C-m",
	} {
		if !strings.Contains(argsLog, want) {
			t.Fatalf("tmux log = %q, want to contain %q", argsLog, want)
		}
	}
}
