package e2e_test

import "testing"

// TestHierarchy_ConsumerSideThreeSessionTransportBaseline is a
// consumer-side transport baseline across three session directories,
// extending agentSessionHarness's single-session pattern to three. It is
// NOT #768's AC1/"Required scenarios" bullet 1 (see hierarchyHarness's
// own doc comment, Guardian REVIEW-853 H-2): there is no modeled
// hierarchy, WorkspaceTree, Parent linkage, common session template, or
// alias resolution here -- "top"/"owner"/"repo" are plain session-name
// strings with a hand-built adjacency map. #768's actual AC1/scenario-1,
// and its producer-consumer/authorization requirements, remain OPEN and
// explicitly wait on #764/#765/#766/#767.
//
// What this test DOES establish, with content-level (not count-only)
// assertions:
//  1. A message posted in one session's post/ directory, addressed to a
//     node in a DIFFERENT session's inbox, is delivered through the
//     normal message.DeliverMessage producer/consumer interface across
//     three successive hops (never a synthetic shortcut), with the
//     exact from/to/body content verified at each hop, not just a file
//     count.
//  2. Each hop leaves zero dead letters in both the sending and
//     receiving session -- proving delivery actually succeeded, not
//     merely that DeliverMessage returned nil (which it also does on a
//     dead-letter outcome).
//  3. A message addressed to a recipient the adjacency map does NOT
//     permit is dead-lettered, not delivered -- the one negative-route
//     case this baseline actually tests, supporting the narrower "no
//     misrouting FOR THE ADJACENCY RULES EXERCISED HERE" claim (full
//     authorization-boundary coverage per #768 remains OPEN).
func TestHierarchy_ConsumerSideThreeSessionTransportBaseline(t *testing.T) {
	h := newHierarchyHarness(t)

	// Hop 1: top -> owner.
	h.postAndDeliver(t, hierarchyTopSession, hierarchyOwnerSession+":"+hierarchyMouthpiece, 1, "request: investigate flaky test")
	envelope := h.inboxEnvelope(t, hierarchyOwnerSession)
	assertEnvelopeContains(t, envelope, "from: mouthpiece", "to: level-owner:mouthpiece", "request: investigate flaky test")
	if got := h.inboxCount(t, hierarchyRepoSession); got != 0 {
		t.Fatalf("after hop 1: repo inbox count = %d, want 0 (request has not reached repo yet)", got)
	}
	if got := h.postCount(t, hierarchyTopSession); got != 0 {
		t.Fatalf("after hop 1: top post/ count = %d, want 0 (delivered, not left behind)", got)
	}
	if got := h.deadLetterCount(t, hierarchyTopSession); got != 0 {
		t.Fatalf("after hop 1: top dead-letter count = %d, want 0", got)
	}
	if got := h.deadLetterCount(t, hierarchyOwnerSession); got != 0 {
		t.Fatalf("after hop 1: owner dead-letter count = %d, want 0", got)
	}

	// Hop 2: owner -> repo.
	h.postAndDeliver(t, hierarchyOwnerSession, hierarchyRepoSession+":"+hierarchyMouthpiece, 2, "relay: investigate flaky test")
	envelope = h.inboxEnvelope(t, hierarchyRepoSession)
	assertEnvelopeContains(t, envelope, "from: mouthpiece", "to: level-repo:mouthpiece", "relay: investigate flaky test")
	if got := h.inboxCount(t, hierarchyTopSession); got != 0 {
		t.Fatalf("after hop 2: top inbox count = %d, want 0 (nothing misrouted back yet)", got)
	}
	if got := h.deadLetterCount(t, hierarchyRepoSession); got != 0 {
		t.Fatalf("after hop 2: repo dead-letter count = %d, want 0", got)
	}

	// Hop 3: repo -> owner (the "reviewed result" returning).
	h.postAndDeliver(t, hierarchyRepoSession, hierarchyOwnerSession+":"+hierarchyMouthpiece, 3, "artifact: fix identified and reviewed")
	if got := h.inboxCount(t, hierarchyOwnerSession); got != 2 {
		t.Fatalf("after hop 3: owner inbox count = %d, want 2 (original hop 1 delivery plus this return hop)", got)
	}
	if got := h.deadLetterCount(t, hierarchyOwnerSession); got != 0 {
		t.Fatalf("after hop 3: owner dead-letter count = %d, want 0", got)
	}

	// Hop 4: owner -> top (final report).
	h.postAndDeliver(t, hierarchyOwnerSession, hierarchyTopSession+":"+hierarchyMouthpiece, 4, "report: fix identified and reviewed")
	envelope = h.inboxEnvelope(t, hierarchyTopSession)
	assertEnvelopeContains(t, envelope, "from: mouthpiece", "to: level-top:mouthpiece", "report: fix identified and reviewed")
	if got := h.deadLetterCount(t, hierarchyTopSession); got != 0 {
		t.Fatalf("after hop 4: top dead-letter count = %d, want 0", got)
	}

	// Final state: repo never received anything beyond the single
	// request it was supposed to see, and no post/ directory is left
	// with undelivered files.
	if got := h.inboxCount(t, hierarchyRepoSession); got != 1 {
		t.Fatalf("final: repo inbox count = %d, want 1 (only the original request, never the return report)", got)
	}
	for _, session := range []string{hierarchyTopSession, hierarchyOwnerSession, hierarchyRepoSession} {
		if got := h.postCount(t, session); got != 0 {
			t.Fatalf("final: %s post/ count = %d, want 0 (every posted message was delivered)", session, got)
		}
	}

	// Negative route: repo -> top directly is not in the adjacency map
	// (repo may only reach owner); this must be dead-lettered, not
	// delivered, which is what makes the "no misrouting" claim above
	// meaningful rather than an untested assertion.
	h.postAndDeliver(t, hierarchyRepoSession, hierarchyTopSession+":"+hierarchyMouthpiece, 5, "unauthorized: repo attempting to bypass owner")
	if got := h.inboxCount(t, hierarchyTopSession); got != 1 {
		t.Fatalf("after unauthorized hop: top inbox count = %d, want 1 (unchanged -- the unauthorized message must not be delivered)", got)
	}
	if got := h.deadLetterCount(t, hierarchyRepoSession); got != 1 {
		t.Fatalf("after unauthorized hop: repo dead-letter count = %d, want 1 (the disallowed repo->top message must be dead-lettered)", got)
	}
}
