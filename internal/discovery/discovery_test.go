package discovery

import (
	"testing"
)

func TestDiscoverNodes_WithChildProcess(t *testing.T) {
	// NOTE: This test requires actual tmux panes with child processes
	// Deferred to integration testing
	t.Skip("Requires tmux environment - deferred to integration testing")
}

func TestDiscoverNodes_WithPaneTitle(t *testing.T) {
	// NOTE: This test requires spawning tmux panes with named titles
	// Deferred to integration testing
	t.Skip("Requires tmux environment with named panes - deferred to integration testing")
}

// TestReduceCollisions_TwoPanes verifies that the higher numeric pane ID wins
// in a two-pane collision (e.g., %26 vs %31 → %31 wins).
func TestReduceCollisions_TwoPanes(t *testing.T) {
	nodeKey := "tmux-a2a-postman:boss"
	order := []string{nodeKey}
	candidates := map[string][]paneCandidate{
		nodeKey: {
			{paneID: "%26", paneNum: 26, sessionName: "tmux-a2a-postman", sessionDir: "/base/ctx/tmux-a2a-postman"},
			{paneID: "%31", paneNum: 31, sessionName: "tmux-a2a-postman", sessionDir: "/base/ctx/tmux-a2a-postman"},
		},
	}
	nodes, collisions := reduceCollisions(order, candidates)

	if got := nodes[nodeKey].PaneID; got != "%31" {
		t.Errorf("winner: got %s, want %%31", got)
	}
	if len(collisions) != 1 {
		t.Fatalf("collisions: got %d, want 1", len(collisions))
	}
	if collisions[0].WinnerPaneID != "%31" {
		t.Errorf("WinnerPaneID: got %s, want %%31", collisions[0].WinnerPaneID)
	}
	if collisions[0].LoserPaneID != "%26" {
		t.Errorf("LoserPaneID: got %s, want %%26", collisions[0].LoserPaneID)
	}
	if collisions[0].NodeKey != nodeKey {
		t.Errorf("NodeKey: got %s, want %s", collisions[0].NodeKey, nodeKey)
	}
}

// TestReduceCollisions_NMoreThan2Panes verifies that the highest pane ID wins
// among N>2 colliding panes and that N-1 collision reports are emitted.
func TestReduceCollisions_NMoreThan2Panes(t *testing.T) {
	order := []string{"session:node"}
	candidates := map[string][]paneCandidate{
		"session:node": {
			{paneID: "%10", paneNum: 10, sessionName: "session", sessionDir: "/dir"},
			{paneID: "%5", paneNum: 5, sessionName: "session", sessionDir: "/dir"},
			{paneID: "%20", paneNum: 20, sessionName: "session", sessionDir: "/dir"},
		},
	}
	nodes, collisions := reduceCollisions(order, candidates)

	if got := nodes["session:node"].PaneID; got != "%20" {
		t.Errorf("winner: got %s, want %%20", got)
	}
	// N-1 = 2 collision reports for 3 panes
	if len(collisions) != 2 {
		t.Errorf("collisions: got %d, want 2", len(collisions))
	}
	for _, c := range collisions {
		if c.WinnerPaneID != "%20" {
			t.Errorf("WinnerPaneID: got %s, want %%20", c.WinnerPaneID)
		}
	}
}

// TestReduceCollisions_BothParseFailure verifies that when ALL panes fail to parse
// (paneNum = -1 for all), the first-encountered pane wins (tie-breaking by order).
func TestReduceCollisions_BothParseFailure(t *testing.T) {
	order := []string{"session:node"}
	candidates := map[string][]paneCandidate{
		"session:node": {
			{paneID: "invalid1", paneNum: -1, sessionName: "session", sessionDir: "/dir"},
			{paneID: "invalid2", paneNum: -1, sessionName: "session", sessionDir: "/dir"},
		},
	}
	nodes, collisions := reduceCollisions(order, candidates)

	// First-encountered wins when both are tied at -1
	if got := nodes["session:node"].PaneID; got != "invalid1" {
		t.Errorf("tie-break: got %s, want invalid1 (first-encountered)", got)
	}
	if len(collisions) != 1 {
		t.Fatalf("collisions: got %d, want 1", len(collisions))
	}
	if collisions[0].LoserPaneID != "invalid2" {
		t.Errorf("LoserPaneID: got %s, want invalid2", collisions[0].LoserPaneID)
	}
}

// TestReduceCollisions_ParseFailureVsValid verifies that a valid pane ID always beats
// a parse-failure pane ID. With the -1 sentinel, valid paneNum (>= 0) > -1 so the
// valid pane wins via direct numeric comparison — not tie-breaking.
func TestReduceCollisions_ParseFailureVsValid(t *testing.T) {
	order := []string{"session:node"}
	candidates := map[string][]paneCandidate{
		"session:node": {
			// Parse failure listed first (would win under first-encountered tie-break)
			{paneID: "%foo", paneNum: -1, sessionName: "session", sessionDir: "/dir"},
			// Valid pane listed second — must still win because 5 > -1
			{paneID: "%5", paneNum: 5, sessionName: "session", sessionDir: "/dir"},
		},
	}
	nodes, collisions := reduceCollisions(order, candidates)

	// Valid pane wins even though it is second-encountered
	if got := nodes["session:node"].PaneID; got != "%5" {
		t.Errorf("winner: got %s, want %%5 (valid beats parse-failure)", got)
	}
	if len(collisions) != 1 {
		t.Fatalf("collisions: got %d, want 1", len(collisions))
	}
	if collisions[0].LoserPaneID != "%foo" {
		t.Errorf("LoserPaneID: got %s, want %%foo", collisions[0].LoserPaneID)
	}
	if collisions[0].WinnerPaneID != "%5" {
		t.Errorf("WinnerPaneID: got %s, want %%5", collisions[0].WinnerPaneID)
	}
}

// TestReduceCollisions_NoCollision verifies that no collision reports are emitted
// when each nodeKey maps to exactly one pane.
func TestReduceCollisions_NoCollision(t *testing.T) {
	order := []string{"session:worker", "session:orchestrator"}
	candidates := map[string][]paneCandidate{
		"session:worker": {
			{paneID: "%23", paneNum: 23, sessionName: "session", sessionDir: "/dir"},
		},
		"session:orchestrator": {
			{paneID: "%22", paneNum: 22, sessionName: "session", sessionDir: "/dir"},
		},
	}
	nodes, collisions := reduceCollisions(order, candidates)

	if len(nodes) != 2 {
		t.Errorf("nodes: got %d, want 2", len(nodes))
	}
	if len(collisions) != 0 {
		t.Errorf("collisions: got %d, want 0", len(collisions))
	}
}

// TestReduceCollisions_OrderPreserved verifies that CollisionReports across different
// NodeKeys appear in tmux list-panes traversal order (nodeKeyOrder), not map iteration
// order. With two colliding NodeKeys, the first nodeKey's report must appear first.
func TestReduceCollisions_OrderPreserved(t *testing.T) {
	// Simulate two colliding nodeKeys. The order slice defines expected output order.
	// "session:alpha" is encountered first in the scan, "session:beta" second.
	order := []string{"session:alpha", "session:beta"}
	candidates := map[string][]paneCandidate{
		"session:alpha": {
			{paneID: "%1", paneNum: 1, sessionName: "session", sessionDir: "/dir"},
			{paneID: "%3", paneNum: 3, sessionName: "session", sessionDir: "/dir"},
		},
		"session:beta": {
			{paneID: "%2", paneNum: 2, sessionName: "session", sessionDir: "/dir"},
			{paneID: "%4", paneNum: 4, sessionName: "session", sessionDir: "/dir"},
		},
	}
	_, collisions := reduceCollisions(order, candidates)

	if len(collisions) != 2 {
		t.Fatalf("collisions: got %d, want 2", len(collisions))
	}
	// First report must belong to "session:alpha" (first in traversal order)
	if collisions[0].NodeKey != "session:alpha" {
		t.Errorf("collisions[0].NodeKey: got %s, want session:alpha (traversal order)", collisions[0].NodeKey)
	}
	// Second report must belong to "session:beta"
	if collisions[1].NodeKey != "session:beta" {
		t.Errorf("collisions[1].NodeKey: got %s, want session:beta (traversal order)", collisions[1].NodeKey)
	}
}
