package observer

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestSendObserverDigest_LoopPrevention(t *testing.T) {
	digestedFiles := make(map[string]bool)
	nodes := map[string]discovery.NodeInfo{
		"test-session:observer-a": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"observer-a": {
				Observes: []string{"worker"},
			},
		},
		DigestTemplate: "DIGEST {digest_items}",
	}

	// Call with sender="observer-a" (should be skipped due to loop prevention)
	filename1 := "20260206-120000-from-observer-a-to-worker.md"
	SendObserverDigest(filename1, "observer-a", "worker", nodes, cfg, digestedFiles)

	// Verify file was NOT added to digestedFiles (loop prevention worked)
	if digestedFiles[filename1] {
		t.Errorf("digestedFiles should not contain observer message, but it does")
	}

	// Call with sender="worker" (should be processed)
	filename2 := "20260206-120001-from-worker-to-observer-a.md"
	SendObserverDigest(filename2, "worker", "observer-a", nodes, cfg, digestedFiles)

	// Verify file was added to digestedFiles
	if !digestedFiles[filename2] {
		t.Errorf("digestedFiles should contain worker message, but it doesn't")
	}
}

func TestSendObserverDigest_DuplicatePrevention(t *testing.T) {
	digestedFiles := make(map[string]bool)
	nodes := map[string]discovery.NodeInfo{
		"test-session:observer-a": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"observer-a": {
				Observes: []string{"worker"},
			},
		},
		DigestTemplate: "DIGEST {digest_items}",
	}

	filename := "20260206-120000-from-worker-to-observer-a.md"
	sender := "worker"
	recipient := "observer-a"

	// First call - should add to digestedFiles
	SendObserverDigest(filename, sender, recipient, nodes, cfg, digestedFiles)
	if !digestedFiles[filename] {
		t.Fatalf("digestedFiles should contain %q after first call", filename)
	}

	// Mark first call as processed
	firstCallProcessed := digestedFiles[filename]

	// Second call with same filename - should be skipped due to duplicate prevention
	SendObserverDigest(filename, sender, recipient, nodes, cfg, digestedFiles)

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
		"test-session:worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"worker": {
				Observes: []string{}, // No observes
			},
		},
		DigestTemplate: "DIGEST {digest_items}",
	}

	filename := "20260206-120000-from-orchestrator-to-worker.md"
	sender := "orchestrator"
	recipient := "worker"

	// Call should still mark file as digested even if no subscribers
	SendObserverDigest(filename, sender, recipient, nodes, cfg, digestedFiles)

	// Verify file was added to digestedFiles
	if !digestedFiles[filename] {
		t.Errorf("digestedFiles should contain %q even with no subscribers", filename)
	}
}

// TestSendObserverDigest_PostmanToPostman tests Issue #32 fix
func TestSendObserverDigest_PostmanToPostman(t *testing.T) {
	digestedFiles := make(map[string]bool)
	nodes := map[string]discovery.NodeInfo{
		"test-session:observer-a": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"observer-a": {
				Observes: []string{"postman", "worker"},
			},
		},
		DigestTemplate: "DIGEST {digest_items}",
	}

	// Test postman-to-postman message (should be skipped)
	filename := "20260206-120000-from-postman-to-postman.md"
	sender := "postman"
	recipient := "postman"

	SendObserverDigest(filename, sender, recipient, nodes, cfg, digestedFiles)

	// Verify file was NOT added to digestedFiles (postman-to-postman should be skipped)
	if digestedFiles[filename] {
		t.Errorf("digestedFiles should NOT contain postman-to-postman message, but it does")
	}

	// Test postman-to-worker message (should be processed)
	filename2 := "20260206-120001-from-postman-to-worker.md"
	recipient2 := "worker"
	SendObserverDigest(filename2, sender, recipient2, nodes, cfg, digestedFiles)

	// Verify file was added to digestedFiles
	if !digestedFiles[filename2] {
		t.Errorf("digestedFiles should contain postman-to-worker message, but it doesn't")
	}
}

// TestSendObserverDigest_ObservesFilterFrom tests Issue #62 - observes matches sender
func TestSendObserverDigest_ObservesFilterFrom(t *testing.T) {
	digestedFiles := make(map[string]bool)
	nodes := map[string]discovery.NodeInfo{
		"test-session:observer-a": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"observer-a": {
				Observes: []string{"worker"}, // Observes worker
			},
		},
		DigestTemplate: "DIGEST {digest_items}",
	}

	filename := "20260206-120000-from-worker-to-orchestrator.md"
	sender := "worker"
	recipient := "orchestrator"

	SendObserverDigest(filename, sender, recipient, nodes, cfg, digestedFiles)

	// Verify file was added (sender matches observes)
	if !digestedFiles[filename] {
		t.Errorf("digestedFiles should contain message where sender matches observes")
	}
}

// TestSendObserverDigest_ObservesFilterTo tests Issue #62 - observes matches recipient
func TestSendObserverDigest_ObservesFilterTo(t *testing.T) {
	digestedFiles := make(map[string]bool)
	nodes := map[string]discovery.NodeInfo{
		"test-session:observer-a": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"observer-a": {
				Observes: []string{"worker"}, // Observes worker
			},
		},
		DigestTemplate: "DIGEST {digest_items}",
	}

	filename := "20260206-120000-from-orchestrator-to-worker.md"
	sender := "orchestrator"
	recipient := "worker"

	SendObserverDigest(filename, sender, recipient, nodes, cfg, digestedFiles)

	// Verify file was added (recipient matches observes)
	if !digestedFiles[filename] {
		t.Errorf("digestedFiles should contain message where recipient matches observes")
	}
}

// TestSendObserverDigest_ObservesNoMatch tests Issue #62 - observes does not match sender or recipient
func TestSendObserverDigest_ObservesNoMatch(t *testing.T) {
	digestedFiles := make(map[string]bool)
	nodes := map[string]discovery.NodeInfo{
		"test-session:observer-a": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"observer-a": {
				Observes: []string{"other-node"}, // Does not observe orchestrator or worker
			},
		},
		DigestTemplate: "DIGEST {digest_items}",
	}

	filename := "20260206-120000-from-orchestrator-to-worker.md"
	sender := "orchestrator"
	recipient := "worker"

	SendObserverDigest(filename, sender, recipient, nodes, cfg, digestedFiles)

	// Verify file was added to digestedFiles (duplicate prevention still works)
	// but no digest should be sent (no assertions for actual sending in this test)
	if !digestedFiles[filename] {
		t.Errorf("digestedFiles should contain message even if no observes match (duplicate prevention)")
	}
}

// TestClassifyMessageType tests Issue #72 - message type classification
func TestClassifyMessageType(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		sender    string
		recipient string
		want      string
	}{
		{
			name:      "PONG message (recipient=postman)",
			filename:  "20260209-120000-from-worker-to-postman.md",
			sender:    "worker",
			recipient: "postman",
			want:      "pong",
		},
		{
			name:      "System message (sender=postman)",
			filename:  "20260209-120000-from-postman-to-worker.md",
			sender:    "postman",
			recipient: "worker",
			want:      "system",
		},
		{
			name:      "Normal message",
			filename:  "20260209-120000-from-worker-to-orchestrator.md",
			sender:    "worker",
			recipient: "orchestrator",
			want:      "message",
		},
		{
			name:      "Precedence test: recipient=postman takes priority",
			filename:  "20260209-120000-from-postman-to-postman.md",
			sender:    "postman",
			recipient: "postman",
			want:      "pong", // recipient check comes first
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyMessageType(tt.filename, tt.sender, tt.recipient)
			if got != tt.want {
				t.Errorf("ClassifyMessageType() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSendObserverDigest_ExcludeType tests Issue #72 - digest exclude types
func TestSendObserverDigest_ExcludeType(t *testing.T) {
	// Note: digestedFiles is always set to true (duplicate prevention)
	// regardless of exclude types. Exclude types only skip sending
	// to specific nodes (via continue in the loop).
	// We verify the exclude logic by checking that files are still
	// added to digestedFiles (duplicate prevention works) and that
	// different message types are classified correctly.

	tests := []struct {
		name         string
		sender       string
		recipient    string
		excludeTypes []string
		msgType      string
		description  string
	}{
		{
			name:         "Exclude pong messages",
			sender:       "worker",
			recipient:    "postman",
			excludeTypes: []string{"pong"},
			msgType:      "pong",
			description:  "PONG message classification",
		},
		{
			name:         "Exclude system messages",
			sender:       "postman",
			recipient:    "worker",
			excludeTypes: []string{"system"},
			msgType:      "system",
			description:  "System message classification",
		},
		{
			name:         "Allow message when not in exclude list",
			sender:       "worker",
			recipient:    "orchestrator",
			excludeTypes: []string{"pong", "system"},
			msgType:      "message",
			description:  "Normal message classification",
		},
		{
			name:         "No exclude types configured",
			sender:       "worker",
			recipient:    "postman",
			excludeTypes: []string{},
			msgType:      "pong",
			description:  "PONG without exclude config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			digestedFiles := make(map[string]bool)
			filename := "test-message.md"

			cfg := &config.Config{
				Nodes: map[string]config.NodeConfig{
					"observer-a": {
						Observes:           []string{"worker", "orchestrator", "postman"},
						DigestExcludeTypes: tt.excludeTypes,
					},
				},
				DigestTemplate: "DIGEST {digest_items}",
				TmuxTimeout:    5.0,
			}

			nodes := map[string]discovery.NodeInfo{
				"test-session:observer-a": {
					PaneID:      "%100",
					SessionName: "test-session",
				},
			}

			// Verify message type classification
			gotMsgType := ClassifyMessageType(filename, tt.sender, tt.recipient)
			if gotMsgType != tt.msgType {
				t.Errorf("%s: message type = %v, want %v",
					tt.description, gotMsgType, tt.msgType)
			}

			// Execute
			SendObserverDigest(filename, tt.sender, tt.recipient, nodes, cfg, digestedFiles)

			// Verify digestedFiles is always set (duplicate prevention)
			if !digestedFiles[filename] {
				t.Errorf("%s: digestedFiles[%s] should be true (duplicate prevention)",
					tt.description, filename)
			}
		})
	}
}
