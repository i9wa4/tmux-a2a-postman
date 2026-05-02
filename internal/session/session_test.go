package session

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func alwaysEnabled(_ string) bool  { return true }
func alwaysDisabled(_ string) bool { return false }

func TestBuildSessionList_Empty(t *testing.T) {
	got := BuildSessionList(nil, nil, alwaysEnabled)
	if len(got) != 0 {
		t.Errorf("expected empty list, got %v", got)
	}
}

func TestBuildSessionList_AllSessionsIncluded(t *testing.T) {
	// Sessions in allSessions but no A2A nodes → NodeCount=0
	allSessions := []string{"alpha", "beta"}
	got := BuildSessionList(nil, allSessions, alwaysDisabled)
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(got))
	}
	for _, s := range got {
		if s.NodeCount != 0 {
			t.Errorf("session %q: expected NodeCount=0, got %d", s.Name, s.NodeCount)
		}
		if s.Enabled {
			t.Errorf("session %q: expected Enabled=false", s.Name)
		}
	}
}

func TestBuildSessionList_NodeCount(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"main:orchestrator": {PaneID: "%1", SessionName: "main"},
		"main:worker":       {PaneID: "%2", SessionName: "main"},
		"bg:observer":       {PaneID: "%3", SessionName: "bg"},
	}
	allSessions := []string{"main", "bg"}
	got := BuildSessionList(nodes, allSessions, alwaysEnabled)

	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(got))
	}

	counts := make(map[string]int)
	for _, s := range got {
		counts[s.Name] = s.NodeCount
	}
	if counts["main"] != 2 {
		t.Errorf("expected main NodeCount=2, got %d", counts["main"])
	}
	if counts["bg"] != 1 {
		t.Errorf("expected bg NodeCount=1, got %d", counts["bg"])
	}
}

func TestBuildSessionList_EnabledStatus(t *testing.T) {
	allSessions := []string{"alpha", "beta"}
	isEnabled := func(name string) bool { return name == "alpha" }

	got := BuildSessionList(nil, allSessions, isEnabled)
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(got))
	}

	enabled := make(map[string]bool)
	for _, s := range got {
		enabled[s.Name] = s.Enabled
	}
	if !enabled["alpha"] {
		t.Error("expected alpha to be enabled")
	}
	if enabled["beta"] {
		t.Error("expected beta to be disabled")
	}
}

func TestBuildSessionList_PreservesTmuxSessionOrder(t *testing.T) {
	allSessions := []string{"zebra", "apple", "mango"}
	got := BuildSessionList(nil, allSessions, alwaysEnabled)

	if len(got) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(got))
	}

	want := []string{"zebra", "apple", "mango"}
	for i, s := range got {
		if s.Name != want[i] {
			t.Errorf("position %d: expected %q, got %q", i, want[i], s.Name)
		}
	}
}

func TestBuildSessionList_NodeWithoutSession(t *testing.T) {
	// Node key with no colon is ignored for session counting
	nodes := map[string]discovery.NodeInfo{
		"nocolon": {PaneID: "%1", SessionName: "x"},
	}
	allSessions := []string{"x"}
	got := BuildSessionList(nodes, allSessions, alwaysEnabled)

	if len(got) != 1 {
		t.Fatalf("expected 1 session, got %d", len(got))
	}
	if got[0].NodeCount != 0 {
		t.Errorf("expected NodeCount=0 for malformed node key, got %d", got[0].NodeCount)
	}
}

func TestBuildSessionList_IncludesNodeSessionMissingFromTmuxList(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"zeta:worker": {PaneID: "%1", SessionName: "zeta"},
		"alpha:boss":  {PaneID: "%2", SessionName: "alpha"},
	}

	got := BuildSessionList(nodes, []string{"main"}, alwaysEnabled)

	wantNames := []string{"main", "alpha", "zeta"}
	if len(got) != len(wantNames) {
		t.Fatalf("len(got) = %d, want %d (%#v)", len(got), len(wantNames), got)
	}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Fatalf("got[%d].Name = %q, want %q (full=%#v)", i, got[i].Name, want, got)
		}
	}
	if got[1].NodeCount != 1 || got[2].NodeCount != 1 {
		t.Fatalf("missing session node counts = %d/%d, want 1/1", got[1].NodeCount, got[2].NodeCount)
	}
}

func TestBuildRegistry_ReturnsRecordsCopy(t *testing.T) {
	registry := BuildRegistry(map[string]discovery.NodeInfo{
		"main:worker": {PaneID: "%1", SessionName: "main"},
	}, []string{"main"}, alwaysEnabled)

	records := registry.Records()
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].Key != Key("main") {
		t.Fatalf("Key = %q, want main", records[0].Key)
	}

	records[0].Name = "mutated"
	again := registry.Records()
	if again[0].Name != "main" {
		t.Fatalf("registry Records leaked backing slice, name = %q", again[0].Name)
	}
}

func TestBuildSessionList_ReturnType(t *testing.T) {
	// Verify return type is []tui.SessionInfo (compile-time check)
	_ = BuildSessionList(nil, nil, alwaysEnabled)
}
