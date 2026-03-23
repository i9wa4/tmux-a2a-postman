package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestReduceCollisions_NeverSetsIsPhony verifies the §3.7 ownership invariant:
// reduceCollisions (the only NodeInfo construction path) never sets IsPhony: true.
func TestReduceCollisions_NeverSetsIsPhony(t *testing.T) {
	order := []string{"session:worker", "session:orchestrator", "session:boss"}
	candidates := map[string][]paneCandidate{
		"session:worker": {
			{paneID: "%10", paneNum: 10, sessionName: "session", sessionDir: "/dir"},
			{paneID: "%20", paneNum: 20, sessionName: "session", sessionDir: "/dir"},
		},
		"session:orchestrator": {
			{paneID: "%5", paneNum: 5, sessionName: "session", sessionDir: "/dir"},
		},
		"session:boss": {
			{paneID: "%invalid", paneNum: -1, sessionName: "session", sessionDir: "/dir"},
		},
	}
	nodes, _ := reduceCollisions(order, candidates)

	for key, info := range nodes {
		if info.IsPhony {
			t.Errorf("reduceCollisions set IsPhony: true on %q — only binding.Load may do this", key)
		}
	}
}

// TestDiscoverNodesWithCollisions_NeverPhony verifies the §3.7 ownership invariant
// at the public API level. Skipped because DiscoverNodesWithCollisions requires a
// live tmux environment; the invariant is structurally covered by
// TestReduceCollisions_NeverSetsIsPhony (the only NodeInfo construction path).
func TestDiscoverNodesWithCollisions_NeverPhony(t *testing.T) {
	t.Skip("Requires tmux environment — invariant covered by TestReduceCollisions_NeverSetsIsPhony")
}

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

// TestResolveNodeName_AlreadyPrefixed verifies that an already-prefixed name is returned as-is
// when found in knownNodes.
func TestResolveNodeName_AlreadyPrefixed(t *testing.T) {
	knownNodes := map[string]NodeInfo{
		"sess:node": {PaneID: "%1", SessionName: "sess", SessionDir: "/dir"},
	}
	got := ResolveNodeName("sess:node", "sess", knownNodes)
	if got != "sess:node" {
		t.Errorf("got %q, want %q", got, "sess:node")
	}
}

// TestResolveNodeName_SameSessionPriority verifies that same-session match is preferred
// over cross-session match.
func TestResolveNodeName_SameSessionPriority(t *testing.T) {
	knownNodes := map[string]NodeInfo{
		"sess-a:node": {PaneID: "%1", SessionName: "sess-a", SessionDir: "/dir-a"},
		"sess-b:node": {PaneID: "%2", SessionName: "sess-b", SessionDir: "/dir-b"},
	}
	got := ResolveNodeName("node", "sess-a", knownNodes)
	if got != "sess-a:node" {
		t.Errorf("got %q, want %q (same-session priority)", got, "sess-a:node")
	}
}

// TestResolveNodeName_CrossSessionFallback verifies that cross-session fallback
// is NOT performed: a bare node name not found in the source session must return "".
// F2 fix: cross-session routing requires explicit "session:node" syntax.
func TestResolveNodeName_CrossSessionFallback(t *testing.T) {
	knownNodes := map[string]NodeInfo{
		"sess-b:node": {PaneID: "%2", SessionName: "sess-b", SessionDir: "/dir-b"},
	}
	got := ResolveNodeName("node", "sess-a", knownNodes)
	if got != "" {
		t.Errorf("got %q, want %q (cross-session fallback must be disabled)", got, "")
	}
}

// TestResolveNodeName_Unknown verifies that an unknown node returns an empty string.
func TestResolveNodeName_Unknown(t *testing.T) {
	knownNodes := map[string]NodeInfo{
		"sess:other": {PaneID: "%1", SessionName: "sess", SessionDir: "/dir"},
	}
	got := ResolveNodeName("notfound", "sess", knownNodes)
	if got != "" {
		t.Errorf("got %q, want empty string for unknown node", got)
	}
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

// mockTmuxRunner creates a tmuxRunner for unit testing.
// listPanesOut is returned verbatim for "list-panes" calls.
// Since M2 batches @a2a_context_id into the list-panes format string,
// show-options is no longer called; any call to it causes the test to fail.
func mockTmuxRunner(listPanesOut string) tmuxRunner {
	return func(args ...string) ([]byte, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("mockTmuxRunner: no args")
		}
		switch args[0] {
		case "list-panes":
			return []byte(listPanesOut), nil
		default:
			return nil, fmt.Errorf("mockTmuxRunner: unexpected subcommand %q (show-options should not be called after M2)", args[0])
		}
	}
}

// tabLine builds a tab-delimited list-panes output line for tests.
// Fields: paneID, claimedContext (empty = unclaimed), sessionName, paneTitle.
func tabLine(paneID, claimedContext, sessionName, paneTitle string) string {
	return paneID + "\t" + claimedContext + "\t" + sessionName + "\t" + paneTitle
}

// mustMkdirAll creates dir and all parents; fails the test on error.
func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
}

// TestFilterLoop_ForeignMatchingContext verifies that a foreign-session pane whose
// @a2a_context_id matches the daemon's contextID is included in discovery results.
func TestFilterLoop_ForeignMatchingContext(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-a2a"
	selfSession := "sess-daemon"
	sessionName := "sess-other"
	mustMkdirAll(t, filepath.Join(baseDir, contextID, sessionName, "inbox"))

	runner := mockTmuxRunner(tabLine("%10", contextID, sessionName, "worker"))
	nodes, _, err := discoverNodesWithCollisionsUsing(runner, baseDir, contextID, selfSession)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := nodes[sessionName+":worker"]; !ok {
		t.Errorf("expected %q in nodes (matching context), got keys: %v", sessionName+":worker", nodeKeys(nodes))
	}
}

// TestFilterLoop_ForeignDifferentContext verifies that a foreign-session pane owned
// by a different daemon context is excluded from discovery results (F3 guard).
func TestFilterLoop_ForeignDifferentContext(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-a2a"
	selfSession := "sess-daemon"
	sessionName := "sess-other"
	mustMkdirAll(t, filepath.Join(baseDir, contextID, sessionName, "inbox"))

	runner := mockTmuxRunner(tabLine("%11", "ctx-other", sessionName, "orchestrator"))
	nodes, _, err := discoverNodesWithCollisionsUsing(runner, baseDir, contextID, selfSession)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := nodes[sessionName+":orchestrator"]; ok {
		t.Errorf("expected %q to be excluded (different context), but it was included", sessionName+":orchestrator")
	}
}

// TestFilterLoop_UnclaimedPane verifies that a foreign-session pane with an empty
// @a2a_context_id field (unclaimed) is included in discovery results.
func TestFilterLoop_UnclaimedPane(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-a2a"
	selfSession := "sess-daemon"
	sessionName := "sess-other"
	mustMkdirAll(t, filepath.Join(baseDir, contextID, sessionName, "inbox"))

	// Empty claimedContext field → unclaimed pane, must be included.
	runner := mockTmuxRunner(tabLine("%12", "", sessionName, "critic"))
	nodes, _, err := discoverNodesWithCollisionsUsing(runner, baseDir, contextID, selfSession)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := nodes[sessionName+":critic"]; !ok {
		t.Errorf("expected %q in nodes (unclaimed pane), got keys: %v", sessionName+":critic", nodeKeys(nodes))
	}
}

// TestFilterLoop_OwnSessionFastPath verifies that own-session panes are always
// included without an ownership check (selfSession fast-path).
func TestFilterLoop_OwnSessionFastPath(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-a2a"
	selfSession := "sess-daemon"
	mustMkdirAll(t, filepath.Join(baseDir, contextID, selfSession, "inbox"))

	// Own-session pane with empty claimedContext — fast-path bypasses the check.
	runner := mockTmuxRunner(tabLine("%13", "", selfSession, "boss"))
	nodes, _, err := discoverNodesWithCollisionsUsing(runner, baseDir, contextID, selfSession)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := nodes[selfSession+":boss"]; !ok {
		t.Errorf("expected %q in nodes (own-session fast-path), got keys: %v", selfSession+":boss", nodeKeys(nodes))
	}
}

// nodeKeys returns a slice of keys from a NodeInfo map for test error messages.
func nodeKeys(nodes map[string]NodeInfo) []string {
	keys := make([]string, 0, len(nodes))
	for k := range nodes {
		keys = append(keys, k)
	}
	return keys
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
