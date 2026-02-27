package envelope

import (
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestBuildEnvelope_BasicExpansion(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	pongActiveNodes := map[string]bool{}

	result := BuildEnvelope(cfg, "PING {node} in {context_id}", "worker", "postman", "test-ctx", "task-1", "/session/post/file.md", []string{"worker"}, adjacency, nodes, "", pongActiveNodes)

	if result != "PING worker in test-ctx" {
		t.Errorf("BuildEnvelope() = %q, want %q", result, "PING worker in test-ctx")
	}
}

func TestBuildEnvelope_NoVariables(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	pongActiveNodes := map[string]bool{}

	result := BuildEnvelope(cfg, "PING message", "worker", "postman", "ctx", "", "/session/post/file.md", nil, adjacency, nodes, "", pongActiveNodes)

	if result != "PING message" {
		t.Errorf("BuildEnvelope() = %q, want %q", result, "PING message")
	}
}

func TestBuildEnvelope_MissingVariable(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	pongActiveNodes := map[string]bool{}

	result := BuildEnvelope(cfg, "PING {node} in {missing}", "worker", "postman", "ctx", "", "/session/post/file.md", nil, adjacency, nodes, "", pongActiveNodes)

	if !strings.Contains(result, "PING worker") {
		t.Errorf("BuildEnvelope() = %q, want to contain 'PING worker'", result)
	}
	if !strings.Contains(result, "{missing}") {
		t.Errorf("BuildEnvelope() = %q, want to contain literal '{missing}'", result)
	}
}

func TestBuildEnvelope_MaterializedPath(t *testing.T) {
	matPath := "/fake/path/to/worker.md"
	cfg := &config.Config{
		TmuxTimeout: 5.0,
		MaterializedPaths: map[string]string{
			"worker": matPath,
		},
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{
		"test:worker": {PaneID: "%1", SessionName: "test"},
	}
	pongActiveNodes := map[string]bool{}

	result := BuildEnvelope(cfg, "header\n{template}", "worker", "postman", "ctx", "", "/session/post/file.md", nil, adjacency, nodes, "test", pongActiveNodes)

	if !strings.Contains(result, "Role template: "+matPath) {
		t.Errorf("expected labeled path in result, got: %q", result)
	}
	if strings.Contains(result, "@"+matPath) {
		t.Errorf("result must not contain @path (triggers autocomplete): %q", result)
	}
}

func TestBuildEnvelope_SentinelObfuscation(t *testing.T) {
	nodeTemplate := "# WORKER\n<!-- end of message -->\nSome content"
	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"worker": {Template: nodeTemplate},
		},
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	pongActiveNodes := map[string]bool{}

	result := BuildEnvelope(cfg, "<!-- message start -->\n{template}\n<!-- end of message -->\n", "worker", "postman", "ctx", "", "/session/post/file.md", nil, adjacency, nodes, "", pongActiveNodes)

	if strings.Contains(result, "# WORKER\n<!-- end of message -->") {
		t.Errorf("user template sentinel was not obfuscated; result: %q", result)
	}
	if !strings.Contains(result, "<!-- end of msg -->") {
		t.Errorf("expected obfuscated sentinel in result; got: %q", result)
	}
	if !strings.HasSuffix(strings.TrimRight(result, "\n"), "<!-- end of message -->") {
		t.Errorf("protocol sentinel was altered or missing; result: %q", result)
	}
}

func TestBuildEnvelope_TalksToLine(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}
	adjacency := map[string][]string{
		"worker": {"orchestrator", "observer"},
	}
	nodes := map[string]discovery.NodeInfo{
		"test:orchestrator": {PaneID: "%2", SessionName: "test"},
		"test:observer":     {PaneID: "%3", SessionName: "test"},
	}
	pongActiveNodes := map[string]bool{
		"test:orchestrator": true,
	}

	result := BuildEnvelope(cfg, "msg: {talks_to_line}", "worker", "postman", "ctx", "", "/session/post/file.md", nil, adjacency, nodes, "test", pongActiveNodes)

	if !strings.Contains(result, "orchestrator") {
		t.Errorf("result = %q, want to contain 'orchestrator'", result)
	}
	if strings.Contains(result, "observer") {
		t.Errorf("result = %q, should not contain 'observer' (not PONG-active)", result)
	}
}

func TestBuildEnvelope_InboxPath(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	pongActiveNodes := map[string]bool{}

	result := BuildEnvelope(cfg, "inbox: {inbox_path}", "worker", "postman", "ctx", "", "/my/session/post/file.md", nil, adjacency, nodes, "", pongActiveNodes)

	if strings.Contains(result, "{inbox_path}") {
		t.Errorf("inbox_path was not expanded: %q", result)
	}
	if !strings.Contains(result, "/my/session/inbox/worker") {
		t.Errorf("result = %q, want to contain '/my/session/inbox/worker'", result)
	}
}
