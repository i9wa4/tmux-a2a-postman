package e2e_test

import "testing"

// TestHierarchy_RequestTraversesThreeLevelsAndReturns exercises #768's
// first acceptance-criteria bullet and first "Required scenarios" bullet
// (happy path only; concurrent-flow, restart/timeout, reconnect, and
// unauthorized-relay scenarios explicitly wait on #764/#765/#766/#767 per
// the design artifact this test implements):
//
//  1. A request enters at the top-level mouthpiece.
//  2. It traverses owner/group and repository delegation, hop by hop,
//     through the normal post/->inbox/ producer/consumer interface
//     (message.DeliverMessage), never a synthetic shortcut.
//  3. The repository level produces a reviewed artifact/result.
//  4. The result returns to the original top-level mouthpiece, hop by
//     hop, the same way.
//  5. No message is ever misrouted to a level the request never visited.
func TestHierarchy_RequestTraversesThreeLevelsAndReturns(t *testing.T) {
	h := newHierarchyHarness(t)

	// Step 1-3: top -> owner -> repo request traversal.
	h.postAndDeliver(t, hierarchyTopSession, hierarchyOwnerSession+":"+hierarchyMouthpiece, 1, "request: investigate flaky test")
	if got := h.inboxCount(t, hierarchyOwnerSession); got != 1 {
		t.Fatalf("after hop 1: owner inbox count = %d, want 1", got)
	}
	if got := h.inboxCount(t, hierarchyRepoSession); got != 0 {
		t.Fatalf("after hop 1: repo inbox count = %d, want 0 (request has not reached repo yet)", got)
	}
	if got := h.postCount(t, hierarchyTopSession); got != 0 {
		t.Fatalf("after hop 1: top post/ count = %d, want 0 (delivered, not left behind)", got)
	}

	h.postAndDeliver(t, hierarchyOwnerSession, hierarchyRepoSession+":"+hierarchyMouthpiece, 2, "relay: investigate flaky test")
	if got := h.inboxCount(t, hierarchyRepoSession); got != 1 {
		t.Fatalf("after hop 2: repo inbox count = %d, want 1", got)
	}
	if got := h.inboxCount(t, hierarchyTopSession); got != 0 {
		t.Fatalf("after hop 2: top inbox count = %d, want 0 (nothing misrouted back yet)", got)
	}

	// Step 4-5: repo produces a reviewed result and it returns, hop by
	// hop, repo -> owner -> top.
	h.postAndDeliver(t, hierarchyRepoSession, hierarchyOwnerSession+":"+hierarchyMouthpiece, 3, "artifact: fix identified and reviewed")
	if got := h.inboxCount(t, hierarchyOwnerSession); got != 2 {
		t.Fatalf("after hop 3: owner inbox count = %d, want 2 (original hop 1 delivery plus this return hop)", got)
	}
	if got := h.inboxCount(t, hierarchyTopSession); got != 0 {
		t.Fatalf("after hop 3: top inbox count = %d, want 0 (result not yet returned)", got)
	}

	h.postAndDeliver(t, hierarchyOwnerSession, hierarchyTopSession+":"+hierarchyMouthpiece, 4, "report: fix identified and reviewed")
	if got := h.inboxCount(t, hierarchyTopSession); got != 1 {
		t.Fatalf("after hop 4: top inbox count = %d, want 1 (final report delivered)", got)
	}

	// Regression-baseline assertion (matches TestE2E_BasicRouting's own
	// "verify message not delivered elsewhere" pattern): the repo level
	// never received anything beyond the single request it was supposed
	// to see, and no post/ directory is left with undelivered files.
	if got := h.inboxCount(t, hierarchyRepoSession); got != 1 {
		t.Fatalf("final: repo inbox count = %d, want 1 (only the original request, never the return report)", got)
	}
	for _, session := range []string{hierarchyTopSession, hierarchyOwnerSession, hierarchyRepoSession} {
		if got := h.postCount(t, session); got != 0 {
			t.Fatalf("final: %s post/ count = %d, want 0 (every posted message was delivered)", session, got)
		}
	}
}
