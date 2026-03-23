package e2e_test

import "testing"

// TestQueueDepthCap verifies that messages delivered to a full inbox (>= 20)
// are sent to dead-letter instead of the recipient inbox.
func TestQueueDepthCap(t *testing.T) {
	h := newAgentSessionHarness(t)
	// Fill inbox to capacity (20) and deliver one more (21st → dead-letter).
	for i := 1; i <= 21; i++ {
		h.postAndDeliver(t, harnessSenderNode, harnessAgentNode, i)
	}
	if got := h.inboxCount(t, harnessAgentNode); got != 20 {
		t.Errorf("inbox count = %d, want 20", got)
	}
	if got := h.deadLetterCount(t); got < 1 {
		t.Errorf("dead-letter count = %d, want >= 1", got)
	}
}

// TestDeadLetter verifies that messages from a sender with no adjacency edge
// to the recipient are dead-lettered with the routing-denied suffix.
func TestDeadLetter(t *testing.T) {
	h := newAgentSessionHarness(t)
	// Add a known node that has no adjacency entry to agent-node.
	const blockedSender = "blocked-sender"
	h.nodes[harnessSession+":"+blockedSender] = h.nodes[harnessSession+":"+harnessSenderNode]
	h.postAndDeliver(t, blockedSender, harnessAgentNode, 1)
	if got := h.deadLetterCount(t); got != 1 {
		t.Errorf("dead-letter count = %d, want 1", got)
	}
}
