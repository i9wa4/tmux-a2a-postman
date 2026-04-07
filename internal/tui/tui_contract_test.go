package tui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

func sessionRowPattern(cursor string, index int, name string) string {
	return regexp.QuoteMeta(cursor) + `\[` + regexp.QuoteMeta(strconv.Itoa(index)) + `\] ` + regexp.QuoteMeta(name) + `\s+⚪`
}

func requireSessionRow(t *testing.T, view string, cursor string, index int, name string) {
	t.Helper()
	pattern := sessionRowPattern(cursor, index, name)
	matched, err := regexp.MatchString(pattern, view)
	if err != nil {
		t.Fatalf("regexp.MatchString(%q) failed: %v", pattern, err)
	}
	if !matched {
		t.Fatalf("view missing session row pattern %q: %q", pattern, view)
	}
}

func TestTUI_Update_SessionHealthUpdateStoresCanonicalSnapshot(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	event := DaemonEventMsg{
		Type: "session_health_update",
		Details: map[string]interface{}{
			"health": status.SessionHealth{
				SessionName:  "review",
				VisibleState: "composing",
			},
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if got := m.sessionHealth["review"].VisibleState; got != "composing" {
		t.Fatalf("sessionHealth[review].VisibleState = %q, want %q", got, "composing")
	}
}

func TestTUI_View_UsesCanonicalSessionHealthSnapshot(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.sessions = []SessionInfo{
		{Name: "review", Enabled: true},
	}
	m.sessionHealth["review"] = status.SessionHealth{
		SessionName:  "review",
		VisibleState: "spinning",
		Nodes: []status.NodeHealth{
			{Name: "critic", VisibleState: "composing"},
			{Name: "worker", VisibleState: "spinning"},
		},
		Windows: []status.SessionWindow{
			{
				Index: "0",
				Nodes: []status.WindowNode{
					{Name: "critic"},
					{Name: "worker"},
				},
			},
		},
	}

	view := m.View().Content

	if !strings.Contains(view, "> [0] review 🟡") {
		t.Fatalf("view missing canonical session indicator: %q", view)
	}
	if !strings.Contains(view, "critic  🔵  composing") {
		t.Fatalf("view missing composing node row: %q", view)
	}
	if !strings.Contains(view, "worker  🟡  spinning") {
		t.Fatalf("view missing spinning node row: %q", view)
	}
}

func TestTUI_View_WaitsForCanonicalHealthSnapshot(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.sessions = []SessionInfo{
		{Name: "review", Enabled: true},
	}
	m.sessionNodes["review"] = []string{"critic", "worker"}
	m.nodeStates["review:critic"] = "ready"
	m.waitingStates["review:critic"] = "composing"
	m.unreadInboxCounts["review:critic"] = 1

	view := m.View().Content

	if !strings.Contains(view, "> [0] review ⚪") {
		t.Fatalf("view missing loading session indicator: %q", view)
	}
	if !strings.Contains(view, "(loading canonical health)") {
		t.Fatalf("view missing canonical health loading state: %q", view)
	}
	if strings.Contains(view, "critic  🔵  composing") {
		t.Fatalf("view unexpectedly fell back to legacy composing state: %q", view)
	}
}

func TestTUI_View_ShowsUnavailableSessionWithoutCanonicalNodes(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.sessions = []SessionInfo{
		{Name: "review", Enabled: true},
	}
	m.sessionHealth["review"] = status.SessionHealth{
		SessionName:  "review",
		VisibleState: "unavailable",
	}

	view := m.View().Content

	if !strings.Contains(view, "> [0] review ⚪") {
		t.Fatalf("view missing unavailable session indicator: %q", view)
	}
	if !strings.Contains(view, "(session unavailable)") {
		t.Fatalf("view missing unavailable session text: %q", view)
	}
}

func TestTUI_Update_SessionHealthUpdate_RehydratesUnavailableKnownSession(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	statusUpdate := DaemonEventMsg{
		Type: "status_update",
		Details: map[string]interface{}{
			"sessions": []SessionInfo{
				{Name: "ghost", Enabled: true},
				{Name: "review", Enabled: true},
			},
		},
	}

	newModel, _ := m.Update(statusUpdate)
	m = newModel.(Model)

	view := m.View().Content

	requireSessionRow(t, view, "> ", 0, "ghost")
	requireSessionRow(t, view, "  ", 1, "review")

	healthUpdate := DaemonEventMsg{
		Type: "session_health_update",
		Details: map[string]interface{}{
			"health": status.SessionHealth{
				SessionName:  "review",
				VisibleState: "unavailable",
			},
		},
	}

	newModel, _ = m.Update(healthUpdate)
	m = newModel.(Model)

	view = m.View().Content

	requireSessionRow(t, view, "> ", 0, "ghost")
	requireSessionRow(t, view, "  ", 1, "review")
	if !strings.Contains(view, "(loading canonical health)") {
		t.Fatalf("view missing loading state for selected tmux session without health: %q", view)
	}
}

func TestTUI_Update_SessionHealthUpdate_ShowsUnavailableSelectedSessionText(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.knownSessions = []SessionInfo{
		{Name: "ghost", Enabled: true},
		{Name: "review", Enabled: true},
	}
	m.refreshVisibleSessions()
	m.selectedSession = 1

	healthUpdate := DaemonEventMsg{
		Type: "session_health_update",
		Details: map[string]interface{}{
			"health": status.SessionHealth{
				SessionName:  "review",
				VisibleState: "unavailable",
			},
		},
	}

	newModel, _ := m.Update(healthUpdate)
	m = newModel.(Model)

	view := m.View().Content

	requireSessionRow(t, view, "> ", 1, "review")
	if !strings.Contains(view, "(session unavailable)") {
		t.Fatalf("view missing unavailable session text for selected session: %q", view)
	}
}

func TestTUI_Update_StatusUpdate_PreservesTmuxSessionRowsWithoutCanonicalNodes(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	event := DaemonEventMsg{
		Type: "status_update",
		Details: map[string]interface{}{
			"sessions": []SessionInfo{
				{Name: "ghost", Enabled: true},
				{Name: "main", Enabled: true},
			},
			"session_nodes": map[string][]string{
				"main": {"worker", "messenger"},
			},
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	view := m.View().Content

	requireSessionRow(t, view, "> ", 0, "ghost")
	requireSessionRow(t, view, "  ", 1, "main")
}

func TestTUI_Update_StatusUpdate_PreservesExactTmuxSessionOrder(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	event := DaemonEventMsg{
		Type: "status_update",
		Details: map[string]interface{}{
			"sessions": []SessionInfo{
				{Name: "ghost", Enabled: true},
				{Name: "review", Enabled: true},
				{Name: "main", Enabled: true},
			},
			"session_nodes": map[string][]string{
				"review": {"critic", "worker"},
				"main":   {"messenger"},
			},
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	view := m.View().Content

	wantPatterns := []string{
		sessionRowPattern("> ", 0, "ghost"),
		sessionRowPattern("  ", 1, "review"),
		sessionRowPattern("  ", 2, "main"),
	}
	for _, pattern := range wantPatterns {
		matched, err := regexp.MatchString(pattern, view)
		if err != nil {
			t.Fatalf("regexp.MatchString(%q) failed: %v", pattern, err)
		}
		if !matched {
			t.Fatalf("view missing tmux-ordered session row %q: %q", pattern, view)
		}
	}
	ghostPos := strings.Index(view, "[0] ghost")
	reviewPos := strings.Index(view, "[1] review")
	mainPos := strings.Index(view, "[2] main")
	if !(ghostPos >= 0 && reviewPos > ghostPos && mainPos > reviewPos) {
		t.Fatalf("view order does not match tmux session order: %q", view)
	}
}
