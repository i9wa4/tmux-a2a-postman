package projection

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

const (
	compactionTestSession = "session-key"
	compactionTestNode    = "review:worker"
	compactionTestPane    = "%17"
)

var compactionTestOrdinal atomic.Uint64

func compactionTestEvent(t *testing.T, eventType string, generation int, payload journal.CompactionObservationPayload) journal.Event {
	t.Helper()
	if payload.ObservationOrdinal == 0 && (eventType == journal.CompactionObservationPresentEventType || eventType == journal.CompactionObservationAbsentEventType) {
		payload.ObservationOrdinal = compactionTestOrdinal.Add(1)
	}
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
	if newState.CanonicalLifecycle != "pid:200" || newState.ActivePredecessorKey == stable.ActivePredecessorKey || newState.OccurrenceEpoch != stable.OccurrenceEpoch+1 {
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
	if afterB.OccurrenceEpoch != 1 || afterB.ActivePredecessorKey == "" || afterB.ActivePredecessorKey == first.ActivePredecessorKey {
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
			event := compactionTestEvent(t, journal.CompactionObservationAbsentEventType, 2, payload)
			got := compactionStateFor(t, present, event)
			if got.ActivePredecessorKey != first.ActivePredecessorKey || got.OccurrenceEpoch != first.OccurrenceEpoch {
				t.Fatalf("uncertain absence closed predecessor: %+v", got)
			}
			if name == "wrong epoch" {
				if got.LastObservationOrdinal <= first.LastObservationOrdinal {
					t.Fatalf("authoritative snapshot did not fence stale evidence: %+v", got)
				}
			} else if got != first {
				t.Fatalf("uncertain absence changed state: %+v", got)
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
	presence := compactionPresence("pid:100", "A", 1)
	presence.ObservationOrdinal = 1
	if _, err := writer.AppendEvent(journal.CompactionObservationPresentEventType, journal.VisibilityControlPlaneOnly, presence, now); err != nil {
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

func TestCompactionObservationMonotonicRecurrenceAcrossLifecycleSwitch(t *testing.T) {
	a := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", "A", 1))
	b := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:200", "B", 1))
	again := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", "A", 1))
	first := compactionStateFor(t, a)
	switched := compactionStateFor(t, a, b)
	got := compactionStateFor(t, a, b, again)
	if got.ActivePredecessorKey == first.ActivePredecessorKey || got.ActivePredecessorKey == switched.ActivePredecessorKey || got.OccurrenceEpoch <= switched.OccurrenceEpoch {
		t.Fatalf("A->B->A reused an occurrence: first=%+v switched=%+v final=%+v", first, switched, got)
	}
	if replayed := compactionStateFor(t, a, b, again); replayed != got {
		t.Fatalf("replay changed occurrence: %+v != %+v", replayed, got)
	}
}

func TestCompactionObservationDelayedPresenceCannotResurrect(t *testing.T) {
	a := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", "A", 1))
	first := compactionStateFor(t, a)
	absence := journal.CompactionObservationPayload{Version: journal.CompactionObservationVersion, NodeKey: compactionTestNode, PaneID: compactionTestPane, LifecycleToken: "pid:100", PredecessorKey: first.ActivePredecessorKey, ExpectedEpoch: first.OccurrenceEpoch, CaptureSucceeded: true, FullHistory: true, PanePresent: true}
	closeA := compactionTestEvent(t, journal.CompactionObservationAbsentEventType, 2, absence)
	closed := compactionStateFor(t, a, closeA)
	if got := compactionStateFor(t, a, closeA, a); got != closed {
		t.Fatalf("delayed A resurrected after close: %+v != %+v", got, closed)
	}
	b := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:200", "B", 1))
	current := compactionStateFor(t, a, closeA, b)
	if got := compactionStateFor(t, a, closeA, b, a); got != current {
		t.Fatalf("delayed A regressed B: %+v != %+v", got, current)
	}
	again := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, compactionPresence("pid:100", "A", 1))
	if got := compactionStateFor(t, a, closeA, b, a, again); got.ActivePredecessorKey == first.ActivePredecessorKey || got.CanonicalLifecycle != "pid:100" {
		t.Fatalf("newer A was rejected or reused old key: %+v", got)
	}
}

func TestCompactionDeliveryKeyCanonicalizesFingerprintHex(t *testing.T) {
	fingerprint := strings.Repeat("ab", 32)
	lower, err := CompactionDeliveryKey(compactionTestSession, 2, compactionTestNode, compactionTestPane, "pid:100", fingerprint, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	upper, err := CompactionDeliveryKey(compactionTestSession, 2, compactionTestNode, compactionTestPane, "pid:100", strings.ToUpper(fingerprint), 1, 0)
	if err != nil || upper != lower {
		t.Fatalf("same digest bytes produced different keys: %q %q %v", lower, upper, err)
	}
	if _, err := CompactionDeliveryKey(compactionTestSession, 2, compactionTestNode, compactionTestPane, "pid:100", strings.Repeat("zz", 32), 1, 0); err == nil {
		t.Fatal("invalid hex accepted")
	}
}

func TestCompactionObservationAliasBudgetFailsClosed(t *testing.T) {
	alias := func(from, to string) journal.Event {
		return compactionTestEvent(t, journal.CompactionLifecycleAliasedEventType, 2, journal.CompactionObservationPayload{Version: journal.CompactionObservationVersion, NodeKey: compactionTestNode, PaneID: compactionTestPane, LifecycleToken: from, CanonicalToken: to})
	}
	oversize := alias(strings.Repeat("x", 10000), "pid:100")
	if _, err := ReduceCompactionObservationEvents(compactionTestSession, 2, []journal.Event{oversize}); err == nil {
		t.Fatal("oversize alias accepted")
	}
	events := make([]journal.Event, 0, 1024)
	for i := 0; i < 1024; i++ {
		events = append(events, alias(fmt.Sprintf("token:%d", i), "pid:100"))
	}
	if _, err := ReduceCompactionObservationEvents(compactionTestSession, 2, events); err == nil {
		t.Fatal("unbounded aliases accepted")
	}
	chain := make([]journal.Event, 0, 1024)
	for i := 0; i < 1024; i++ {
		chain = append(chain, alias(fmt.Sprintf("token:%d", i), fmt.Sprintf("token:%d", i+1)))
	}
	if _, err := ReduceCompactionObservationEvents(compactionTestSession, 2, chain); err == nil {
		t.Fatal("long alias chain accepted")
	}
}

func TestCompactionObservationOrdinalFenceRejectsReorderAndConflict(t *testing.T) {
	presence := func(lifecycle, marker string, ordinal uint64) journal.Event {
		p := compactionPresence(lifecycle, marker, 1)
		p.ObservationOrdinal = ordinal
		return compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, p)
	}
	a10 := presence("pid:100", "A", 10)
	b20 := presence("pid:200", "B", 20)
	b := compactionStateFor(t, a10, b20)
	mismatch := journal.CompactionObservationPayload{Version: journal.CompactionObservationVersion, NodeKey: compactionTestNode, PaneID: compactionTestPane, LifecycleToken: "pid:100", PredecessorKey: compactionStateFor(t, a10).ActivePredecessorKey, ExpectedEpoch: 0, ObservationOrdinal: 40, CaptureSucceeded: true, FullHistory: true, PanePresent: true}
	absent40 := compactionTestEvent(t, journal.CompactionObservationAbsentEventType, 2, mismatch)
	fenced := compactionStateFor(t, a10, b20, absent40)
	if fenced.ActivePredecessorKey != b.ActivePredecessorKey || fenced.LastObservationOrdinal != 40 {
		t.Fatalf("mismatch did not retain B and high-water: %+v", fenced)
	}
	a30 := presence("pid:100", "A", 30)
	if got := compactionStateFor(t, a10, b20, absent40, a30); got != fenced {
		t.Fatalf("reordered A regressed B: %+v", got)
	}
	a41 := presence("pid:100", "A", 41)
	if got := compactionStateFor(t, a10, b20, absent40, a41); got.ActivePredecessorKey == b.ActivePredecessorKey || got.OccurrenceEpoch <= b.OccurrenceEpoch {
		t.Fatalf("newer recurrence rejected: %+v", got)
	}
	if _, err := ReduceCompactionObservationEvents(compactionTestSession, 2, []journal.Event{a10, presence("pid:100", "B", 10)}); err == nil {
		t.Fatal("conflicting same-ordinal presence accepted")
	}
	if got := compactionStateFor(t, a10, a10); got != compactionStateFor(t, a10) {
		t.Fatalf("identical retry changed state: %+v", got)
	}
}

func TestCompactionObservationAliasByteBudgetAndIdempotence(t *testing.T) {
	alias := func(from, to string) journal.Event {
		return compactionTestEvent(t, journal.CompactionLifecycleAliasedEventType, 2, journal.CompactionObservationPayload{Version: journal.CompactionObservationVersion, NodeKey: compactionTestNode, PaneID: compactionTestPane, LifecycleToken: from, CanonicalToken: to})
	}
	first := alias("pid:100", "endpoint:%17")
	if _, err := ReduceCompactionObservationEvents(compactionTestSession, 2, []journal.Event{first, first}); err != nil {
		t.Fatalf("duplicate alias: %v", err)
	}
	events := make([]journal.Event, 0, 100)
	for i := 0; i < 100; i++ {
		events = append(events, alias(fmt.Sprintf("%03d:%s", i, strings.Repeat("x", 200)), "pid:100"))
	}
	if _, err := ReduceCompactionObservationEvents(compactionTestSession, 2, events); err == nil {
		t.Fatal("alias byte budget was not enforced")
	}
}

func TestCompactionObservationPresenceHexCaseAndMissingOrdinal(t *testing.T) {
	lower := compactionPresence("pid:100", "A", 1)
	lower.ObservationOrdinal = 1
	upper := lower
	upper.MarkerFingerprint = strings.ToUpper(lower.MarkerFingerprint)
	upper.ObservationOrdinal = 2
	first := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, lower)
	second := compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, upper)
	state := compactionStateFor(t, first, second)
	if state.OccurrenceEpoch != 0 || state.ActivePredecessorKey != compactionStateFor(t, first).ActivePredecessorKey {
		t.Fatalf("hex case created a new occurrence: %+v", state)
	}
	missing := lower
	missing.ObservationOrdinal = 0
	encoded, err := json.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	event := first
	event.Payload = encoded
	if _, err := ReduceCompactionObservationEvents(compactionTestSession, 2, []journal.Event{event}); err == nil {
		t.Fatal("missing producer ordinal accepted")
	}
	legacy := lower
	legacy.Version = 1
	encoded, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	event.Payload = encoded
	if _, err := ReduceCompactionObservationEvents(compactionTestSession, 2, []journal.Event{event}); err == nil {
		t.Fatal("legacy version accepted as ordered evidence")
	}
}

func TestCompactionObservationAliasReplayAcrossRecurrence(t *testing.T) {
	alias := compactionTestEvent(t, journal.CompactionLifecycleAliasedEventType, 2, journal.CompactionObservationPayload{Version: journal.CompactionObservationVersion, NodeKey: compactionTestNode, PaneID: compactionTestPane, LifecycleToken: "pid:100", CanonicalToken: "endpoint:%17"})
	a1 := compactionPresence("endpoint:%17", "A", 1)
	a1.ObservationOrdinal = 1
	b := compactionPresence("pid:200", "B", 1)
	b.ObservationOrdinal = 2
	a2 := compactionPresence("pid:100", "A", 1)
	a2.ObservationOrdinal = 3
	events := []journal.Event{compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, a1), alias, compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, b), compactionTestEvent(t, journal.CompactionObservationPresentEventType, 2, a2)}
	first := compactionStateFor(t, events[0])
	final := compactionStateFor(t, events...)
	if final.CanonicalLifecycle != "endpoint:%17" || final.OccurrenceEpoch != 2 || final.ActivePredecessorKey == first.ActivePredecessorKey {
		t.Fatalf("alias recurrence reused key: %+v", final)
	}
	if replayed := compactionStateFor(t, events...); replayed != final {
		t.Fatalf("alias replay changed state: %+v", replayed)
	}
}
