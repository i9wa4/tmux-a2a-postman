package e2e_test

import "testing"

// TestQueueDepthCap verifies that messages delivered to a full inbox (>= 20)
// are sent to dead-letter instead of the recipient inbox.
// Full pass deferred: runs after Milestone 2b completes all stubs.
func TestQueueDepthCap(t *testing.T) {
	t.Skip("stub: full pass verified after Milestone 2b")
	h := newAgentSessionHarness(t)
	_ = h // deliver 21 messages; assert inbox==20 and deadLetter>=1
}

// TestDeadLetter verifies that messages from a disallowed sender are sent to
// dead-letter with the routing-denied suffix.
// Full pass deferred: runs after Milestone 2b completes all stubs.
func TestDeadLetter(t *testing.T) {
	t.Skip("stub: full pass verified after Milestone 2b")
	h := newAgentSessionHarness(t)
	_ = h // deliver from unlisted sender; assert deadLetterCount==1
}
