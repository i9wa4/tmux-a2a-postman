package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
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
	added := []string{}
	addFn := func(path string) error {
		added = append(added, path)
		return nil
	}

	paths, err := ensureWatchedPath([]string{"config.toml"}, "bindings.toml", addFn)
	if err != nil {
		t.Fatalf("ensureWatchedPath() error = %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("ensureWatchedPath() len = %d, want 2", len(paths))
	}
	if len(added) != 1 || added[0] != "bindings.toml" {
		t.Fatalf("ensureWatchedPath() added = %v, want [bindings.toml]", added)
	}

	paths, err = ensureWatchedPath(paths, "bindings.toml", addFn)
	if err != nil {
		t.Fatalf("ensureWatchedPath() duplicate error = %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("ensureWatchedPath() duplicate len = %d, want 2", len(paths))
	}
	if len(added) != 1 {
		t.Fatalf("ensureWatchedPath() duplicate add count = %d, want 1", len(added))
	}
}

func TestRefreshNodesWithRegistry_ReplacesPhonySnapshotOnBindingsChange(t *testing.T) {
	root := t.TempDir()
	bindingsPath := filepath.Join(root, "bindings.toml")
	initialBindings := `[[binding]]
channel_id = "channel-a"
node_name = "channel-a"
context_id = "ctx"
session_name = "external"
pane_title = "channel-a-pane"
pane_node_name = ""
active = true
permitted_senders = ["messenger"]
`
	if err := os.WriteFile(bindingsPath, []byte(initialBindings), 0o600); err != nil {
		t.Fatalf("WriteFile initial bindings: %v", err)
	}

	registry, err := binding.Load(bindingsPath, binding.AllowEmptySenders())
	if err != nil {
		t.Fatalf("binding.Load(initial): %v", err)
	}

	nodes := refreshNodesWithRegistry(map[string]discovery.NodeInfo{
		"test-session:messenger": {},
		"channel-a":              {IsPhony: true},
	}, registry)
	if _, ok := nodes["channel-a"]; !ok {
		t.Fatal("expected phony node to be present after initial registry load")
	}
	if _, ok := nodes["test-session:messenger"]; !ok {
		t.Fatal("expected real node to remain after initial registry load")
	}

	if err := os.WriteFile(bindingsPath, []byte("invalid = ["), 0o600); err != nil {
		t.Fatalf("WriteFile invalid bindings: %v", err)
	}
	if _, err := binding.Load(bindingsPath, binding.AllowEmptySenders()); err == nil {
		t.Fatal("binding.Load(invalid) error = nil, want parse failure")
	}

	nodes = refreshNodesWithRegistry(nodes, nil)
	if _, ok := nodes["channel-a"]; ok {
		t.Fatal("expected stale phony node to be removed after invalid registry reload")
	}
	if _, ok := nodes["test-session:messenger"]; !ok {
		t.Fatal("expected real node to remain after invalid registry reload")
	}
}
