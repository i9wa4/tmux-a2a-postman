package e2e_test

import "testing"

// TestSenderAllowlist verifies that a message from a sender with no adjacency
// edge to the recipient is dead-lettered with the routing-denied suffix.
func TestSenderAllowlist(t *testing.T) {
	h := newAgentSessionHarness(t)
	// Add a known node that is absent from the adjacency map (no edge to agent-node).
	const unlistedSender = "unlisted-sender"
	h.nodes[harnessSession+":"+unlistedSender] = h.nodes[harnessSession+":"+harnessSenderNode]
	h.postAndDeliver(t, unlistedSender, harnessAgentNode, 1)
	if got := h.deadLetterCount(t); got != 1 {
		t.Errorf("dead-letter count = %d, want 1", got)
	}
}
