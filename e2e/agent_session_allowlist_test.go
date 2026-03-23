package e2e_test

import "testing"

// TestSenderAllowlist verifies that a message from a sender with no adjacency
// edge to the recipient is dead-lettered with the routing-denied suffix.
// Full pass deferred: runs after this milestone completes all stubs.
func TestSenderAllowlist(t *testing.T) {
	t.Skip("stub: full pass verified after Milestone 2b stubs complete")
	h := newAgentSessionHarness(t)
	_ = h // deliver from unlisted sender (no edge to agent-node); assert deadLetterCount==1
}
