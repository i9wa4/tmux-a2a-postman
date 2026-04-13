package daemon

import (
	"os"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestFilterNodesByEdges_PreservesSessionPrefixedKeys(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"test-session:messenger":    {},
		"review-session:worker":     {},
		"another-session:critic":    {},
		"test-session:orchestrator": {},
	}

	filterNodesByEdges(nodes, []string{
		"test-session:messenger -- review-session:worker",
		"messenger -- orchestrator",
	})

	if _, ok := nodes["test-session:messenger"]; !ok {
		t.Fatal("expected session-prefixed sender node to remain after edge filtering")
	}
	if _, ok := nodes["review-session:worker"]; !ok {
		t.Fatal("expected session-prefixed recipient node to remain after edge filtering")
	}
	if _, ok := nodes["test-session:orchestrator"]; !ok {
		t.Fatal("expected bare-edge node to remain after edge filtering")
	}
	if _, ok := nodes["another-session:critic"]; ok {
		t.Fatal("unexpected unrelated node remained after edge filtering")
	}
}

func TestRunDaemonLoop_SourceContractReloadsBindingsAndRefreshesPhonyNodes(t *testing.T) {
	sourceBytes, err := os.ReadFile("daemon.go")
	if err != nil {
		t.Fatalf("ReadFile daemon.go: %v", err)
	}
	source := string(sourceBytes)

	if !strings.Contains(source, "watcher.Add(newCfg.BindingsPath)") {
		t.Fatal("daemon.go no longer adds a watcher for bindings-path changes discovered after config reload")
	}
	if !strings.Contains(source, "mergePhonyNodes(freshNodes, registry)") {
		t.Fatal("daemon.go no longer refreshes discovered nodes with the reloaded phony registry")
	}
}
