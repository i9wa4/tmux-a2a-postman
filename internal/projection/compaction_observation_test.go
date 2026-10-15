package projection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

const (
	compactionTestSession = "session-key"
	compactionTestNode    = "review:worker"
	compactionTestPane    = "%17"
)

func compactionTestEvent(t *testing.T, eventType string, generation int, payload journal.CompactionObservationPayload) journal.Event {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return journal.Event{
		Type:       eventType,
		Visibility: journal.VisibilityControlPlaneOnly,
		SessionKey: compactionTestSession,
		Generation: generation,
		OccurredAt: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Payload:    encoded,
	}
}

func compactionPresence(lifecycle, marker string, count int) journal.CompactionObservationPayload {
	return journal.CompactionObservationPayload{
		Version:           journal.CompactionObservationVersion,
		NodeKey:           compactionTestNode,
		PaneID:            compactionTestPane,
		LifecycleToken:    lifecycle,
		MarkerFingerprint: CompactionMarkerFingerprint(marker),
		MarkerCount:       count,
		CaptureSucceeded:  true,
		PanePresent:       true,
	}
}

func compactionStateFor(t *testing.T, events ...journal.Event) CompactionObservationNode {
	t.Helper()
	projected, err := ReduceCompactionObservationEvents(compactionTestSession, 2, events)
	if err != nil {
		t.Fatal(err)
	}
	return projected.Nodes[compactionNodeID(compactionTestNode, compactionTestPane)]
}

func TestCompactionObservationKeyStableAcrossReplayAndScopedByGeneration(t *testing.T) {
	present := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", " Compacted ", 1))
	first := compactionStateFor(t, present)
	if !strings.HasPrefix(first.ActivePredecessorKey, "compaction:v1:") {
		t.Fatalf("key = %q", first.ActivePredecessorKey)
	}
	replayed := compactionStateFor(t, present, present)
	if replayed != first {
		t.Fatalf("replayed = %+v, first = %+v", replayed, first)
	}
	old := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 1, compactionPresence("pid:old", "old", 1))
	if got := compactionStateFor(t, old, present); got != first {
		t.Fatalf("old generation leaked: %+v", got)
	}
	otherGenerationKey, err := CompactionDeliveryKey(compactionTestSession, 3, compactionTestNode, compactionTestPane, "pid:100", CompactionMarkerFingerprint("Compacted"), 1, 0)
	if err != nil || otherGenerationKey == first.ActivePredecessorKey {
		t.Fatalf("generation key = %q, err = %v", otherGenerationKey, err)
	}
	if CompactionMarkerFingerprint(" Compacted ") != CompactionMarkerFingerprint("Compacted") {
		t.Fatal("marker normalization changed fingerprint")
	}
}

func TestCompactionObservationNodeIdentityIsUnambiguous(t *testing.T) {
	firstNode, firstPane := "node\x00pane", "%17"
	secondNode, secondPane := "node", "pane\x00%17"
	if compactionNodeID(firstNode, firstPane) == compactionNodeID(secondNode, secondPane) {
		t.Fatal("distinct node and pane tuples share an identity")
	}
	first := compactionPresence("pid:first", "A", 1)
	first.NodeKey, first.PaneID = firstNode, firstPane
	second := compactionPresence("pid:second", "B", 1)
	second.NodeKey, second.PaneID = secondNode, secondPane
	state, err := ReduceCompactionObservationEvents(compactionTestSession, 2, []journal.Event{
		compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, first),
		compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Nodes) != 2 ||
		state.Nodes[compactionNodeID(firstNode, firstPane)].CanonicalLifecycle != "pid:first" ||
		state.Nodes[compactionNodeID(secondNode, secondPane)].CanonicalLifecycle != "pid:second" {
		t.Fatalf("distinct node and pane observations collided: %+v", state.Nodes)
	}
}

func TestCompactionObservationRejectsContradictoryPresence(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*journal.CompactionObservationPayload)
	}{
		{"capture failed", func(p *journal.CompactionObservationPayload) { p.CaptureSucceeded = false }},
		{"pane absent", func(p *journal.CompactionObservationPayload) { p.PanePresent = false }},
		{"both absent", func(p *journal.CompactionObservationPayload) { p.CaptureSucceeded, p.PanePresent = false, false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := compactionPresence("pid:100", "A", 1)
			tc.edit(&payload)
			_, err := ReduceCompactionObservationEvents(compactionTestSession, 2, []journal.Event{
				compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, payload),
			})
			if err == nil {
				t.Fatal("replay accepted contradictory presence evidence")
			}
		})
	}
}

func TestCompactionObservationLifecycleAliasAndKnownPIDChange(t *testing.T) {
	unknown := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("endpoint:%17", "A", 1))
	aliasKnown := compactionTestEvent(t, journal.CompactionLifecycleAliasedEventType, 2, journal.CompactionObservationPayload{
		Version: journal.CompactionObservationVersion, NodeKey: compactionTestNode, PaneID: compactionTestPane,
		LifecycleToken: "pid:100", CanonicalToken: "endpoint:%17", ObservedPID: "100",
	})
	known := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", "A", 1))
	first := compactionStateFor(t, unknown)
	aliased := compactionStateFor(t, unknown, aliasKnown, known)
	if aliased.ActivePredecessorKey != first.ActivePredecessorKey || aliased.CanonicalLifecycle != "endpoint:%17" {
		t.Fatalf("unknown -> known changed identity: %+v", aliased)
	}

	// A missing PID observation aliases the endpoint token to the known PID.
	knownFirst := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", "A", 1))
	aliasUnknown := compactionTestEvent(t, journal.CompactionLifecycleAliasedEventType, 2, journal.CompactionObservationPayload{
		Version: journal.CompactionObservationVersion, NodeKey: compactionTestNode, PaneID: compactionTestPane,
		LifecycleToken: "endpoint:%17", CanonicalToken: "pid:100",
	})
	unknownAgain := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("endpoint:%17", "A", 1))
	stable := compactionStateFor(t, knownFirst, aliasUnknown, unknownAgain)
	if stable.ActivePredecessorKey != compactionStateFor(t, knownFirst).ActivePredecessorKey {
		t.Fatalf("known -> unknown changed identity: %+v", stable)
	}
	changed := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:200", "A", 1))
	newState := compactionStateFor(t, knownFirst, aliasUnknown, unknownAgain, changed)
	if newState.CanonicalLifecycle != "pid:200" || newState.ActivePredecessorKey == stable.ActivePredecessorKey || newState.OccurrenceEpoch != 0 {
		t.Fatalf("known PID change did not start lifecycle: %+v", newState)
	}
}

func TestCompactionObservationAliasConflictsFailClosed(t *testing.T) {
	alias := func(from, to string) journal.Event {
		return compactionTestEvent(t, journal.CompactionLifecycleAliasedEventType, 2, journal.CompactionObservationPayload{
			Version: journal.CompactionObservationVersion, NodeKey: compactionTestNode, PaneID: compactionTestPane,
			LifecycleToken: from, CanonicalToken: to,
		})
	}
	for name, events := range map[string][]journal.Event{
		"conflict":     {alias("pid:100", "endpoint:a"), alias("pid:100", "endpoint:b")},
		"cycle":        {alias("pid:100", "endpoint:a"), alias("endpoint:a", "pid:100")},
		"active remap": {compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", "A", 1)), alias("pid:100", "endpoint:a")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReduceCompactionObservationEvents(compactionTestSession, 2, events); err == nil {
				t.Fatal("replay accepted conflicting alias")
			}
		})
	}
}

func TestCompactionObservationAuthoritativeAbsenceClosesOnce(t *testing.T) {
	presentA := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", "A", 1))
	first := compactionStateFor(t, presentA)
	absence := journal.CompactionObservationPayload{
		Version: journal.CompactionObservationVersion, NodeKey: compactionTestNode, PaneID: compactionTestPane,
		LifecycleToken: "pid:100", PredecessorKey: first.ActivePredecessorKey, ExpectedEpoch: 0,
		CaptureSucceeded: true, FullHistory: true, PanePresent: true,
	}
	closeA := compactionTestEvent(t, journal.CompactionObservationAbsentEventType, 2, absence)
	closed := compactionStateFor(t, presentA, closeA, closeA)
	if closed.OccurrenceEpoch != 1 || closed.ActivePredecessorKey != "" {
		t.Fatalf("absence did not close exactly once: %+v", closed)
	}
	reappeared := compactionStateFor(t, presentA, closeA, presentA)
	if reappeared.OccurrenceEpoch != 1 || reappeared.ActivePredecessorKey == first.ActivePredecessorKey {
		t.Fatalf("new occurrence reused key: %+v", reappeared)
	}
	presentB := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", "B", 2))
	afterB := compactionStateFor(t, presentA, presentB, closeA)
	if afterB.OccurrenceEpoch != 0 || afterB.ActivePredecessorKey == "" || afterB.ActivePredecessorKey == first.ActivePredecessorKey {
		t.Fatalf("delayed absence closed newer marker: %+v", afterB)
	}
}

func TestCompactionObservationUncertainAbsenceDoesNotAdvance(t *testing.T) {
	present := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", "A", 1))
	first := compactionStateFor(t, present)
	base := journal.CompactionObservationPayload{
		Version: journal.CompactionObservationVersion, NodeKey: compactionTestNode, PaneID: compactionTestPane,
		LifecycleToken: "pid:100", PredecessorKey: first.ActivePredecessorKey, ExpectedEpoch: 0,
		CaptureSucceeded: true, FullHistory: true, PanePresent: true,
	}
	for name, change := range map[string]func(*journal.CompactionObservationPayload){
		"capture failure": func(p *journal.CompactionObservationPayload) { p.CaptureSucceeded = false },
		"tail only":       func(p *journal.CompactionObservationPayload) { p.FullHistory = false },
		"missing pane":    func(p *journal.CompactionObservationPayload) { p.PanePresent = false },
		"wrong epoch":     func(p *journal.CompactionObservationPayload) { p.ExpectedEpoch = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			payload := base
			change(&payload)
			got := compactionStateFor(t, present, compactionTestEvent(t, journal.CompactionObservationAbsentEventType, 2, payload))
			if got != first {
				t.Fatalf("uncertain absence advanced state: %+v", got)
			}
		})
	}
}

func TestProjectCompactionObservationReplaysCurrentJournal(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review")
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	if _, err := ProjectCompactionObservation(sessionDir); err == nil {
		t.Fatal("missing current session was accepted")
	}
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx", "review", os.Getpid(), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AppendEvent(journal.CompactionObservationPresentEventType, journal.VisibilityControlPlaneOnly, compactionPresence("pid:100", "A", 1), now); err != nil {
		t.Fatal(err)
	}
	got, err := ProjectCompactionObservation(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Nodes[compactionNodeID(compactionTestNode, compactionTestPane)].ActivePredecessorKey == "" {
		t.Fatalf("replay has no active predecessor: %+v", got)
	}
}
