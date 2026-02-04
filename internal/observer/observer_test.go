package observer

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestSendObserverDigest_LoopPrevention(t *testing.T) {
	digestedFiles := make(map[string]bool)
	nodes := map[string]discovery.NodeInfo{
		"observer-a": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"observer-a": {
				SubscribeDigest: true,
			},
		},
		DigestTemplate: "DIGEST {digest_items}",
	}

	// Call with sender="observer-a" (should be skipped due to loop prevention)
	SendObserverDigest("msg-from-observer-a.md", "observer-a", nodes, cfg, digestedFiles)

	// Verify file was NOT added to digestedFiles (loop prevention worked)
	if digestedFiles["msg-from-observer-a.md"] {
		t.Errorf("digestedFiles should not contain observer message, but it does")
	}

	// Call with sender="worker" (should be processed)
	SendObserverDigest("msg-from-worker.md", "worker", nodes, cfg, digestedFiles)

	// Verify file was added to digestedFiles
	if !digestedFiles["msg-from-worker.md"] {
		t.Errorf("digestedFiles should contain worker message, but it doesn't")
	}
}

func TestSendObserverDigest_DuplicatePrevention(t *testing.T) {
	digestedFiles := make(map[string]bool)
	nodes := map[string]discovery.NodeInfo{
		"observer-a": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"observer-a": {
				SubscribeDigest: true,
			},
		},
		DigestTemplate: "DIGEST {digest_items}",
	}

	filename := "msg-from-worker.md"
	sender := "worker"

	// First call - should add to digestedFiles
	SendObserverDigest(filename, sender, nodes, cfg, digestedFiles)
	if !digestedFiles[filename] {
		t.Fatalf("digestedFiles should contain %q after first call", filename)
	}

	// Mark first call as processed
	firstCallProcessed := digestedFiles[filename]

	// Second call with same filename - should be skipped due to duplicate prevention
	SendObserverDigest(filename, sender, nodes, cfg, digestedFiles)

	// Verify digestedFiles still contains the file (not removed)
	if !digestedFiles[filename] {
		t.Errorf("digestedFiles should still contain %q after second call", filename)
	}

	// Verify the state hasn't changed (still marked as processed from first call)
	if digestedFiles[filename] != firstCallProcessed {
		t.Errorf("digestedFiles[%q] state should not change on duplicate call", filename)
	}
}

func TestSendObserverDigest_NoSubscribers(t *testing.T) {
	digestedFiles := make(map[string]bool)
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"worker": {
				SubscribeDigest: false, // Not subscribed
			},
		},
		DigestTemplate: "DIGEST {digest_items}",
	}

	filename := "msg-from-orchestrator.md"
	sender := "orchestrator"

	// Call should still mark file as digested even if no subscribers
	SendObserverDigest(filename, sender, nodes, cfg, digestedFiles)

	// Verify file was added to digestedFiles
	if !digestedFiles[filename] {
		t.Errorf("digestedFiles should contain %q even with no subscribers", filename)
	}
}
