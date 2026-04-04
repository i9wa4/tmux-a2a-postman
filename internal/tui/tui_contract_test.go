package tui

import (
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

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
