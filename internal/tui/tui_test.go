package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
)

func TestTUI_InitialModel(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	if m.generalStatus != "Starting..." {
		t.Errorf("initial generalStatus: got %q, want %q", m.generalStatus, "Starting...")
	}
	if m.nodeCount != 0 {
		t.Errorf("initial nodeCount: got %d, want 0", m.nodeCount)
	}
	if len(m.events) != 0 {
		t.Errorf("initial events length: got %d, want 0", len(m.events))
	}
	if m.quitting {
		t.Error("initial quitting: got true, want false")
	}
}

func TestTUI_Update_Quit(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	// Test 'q' key
	newModel, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	m = newModel.(Model)

	if !m.quitting {
		t.Error("quitting flag not set after 'q' key")
	}
	if cmd == nil {
		t.Error("quit command not returned")
	}
}

func TestTUI_Update_MessageReceived(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	// Send message received event
	event := DaemonEventMsg{
		Type:    "message_received",
		Message: "Test message delivered",
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if len(m.events) != 1 {
		t.Errorf("events length: got %d, want 1", len(m.events))
	}
	if m.events[0].Message != "Test message delivered" {
		t.Errorf("event message content: got %q, want %q", m.events[0].Message, "Test message delivered")
	}
	if m.lastEvent != "Test message delivered" {
		t.Errorf("lastEvent: got %q, want %q", m.lastEvent, "Test message delivered")
	}
}

func TestTUI_Update_StatusUpdate(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	// Send status update event
	event := DaemonEventMsg{
		Type:    "status_update",
		Message: "Running",
		Details: map[string]interface{}{
			"node_count": 5,
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if m.generalStatus != "Running" {
		t.Errorf("generalStatus: got %q, want %q", m.generalStatus, "Running")
	}
	if m.nodeCount != 5 {
		t.Errorf("nodeCount: got %d, want 5", m.nodeCount)
	}
}

func TestTUI_View(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.sessions = []SessionInfo{
		{Name: "main", Enabled: true},
		{Name: "review", Enabled: true},
	}
	m.sessionNodes = map[string][]string{
		"main":   {"boss", "messenger"},
		"review": {"critic", "worker"},
	}
	m.sessionSnapshots["main"] = status.SessionStatus{
		SessionName:  "main",
		VisibleState: "ready",
		Nodes: []status.NodeStatus{
			{Name: "boss", VisibleState: "ready"},
			{Name: "messenger", VisibleState: "ready"},
		},
		Windows: []status.SessionWindow{
			{
				Index: "0",
				Nodes: []status.WindowNode{
					{Name: "boss"},
					{Name: "messenger"},
				},
			},
		},
	}
	m.sessionSnapshots["review"] = status.SessionStatus{
		SessionName:  "review",
		VisibleState: "stale",
		Nodes: []status.NodeStatus{
			{Name: "critic", VisibleState: "pending"},
			{Name: "worker", VisibleState: "stale"},
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

	if !strings.Contains(view, "tmux-a2a-postman "+version.Version+"   [up/down:move] [p:ping] [q:quit]") {
		t.Fatalf("view missing simplified header: %q", view)
	}
	if !strings.Contains(view, "[sessions]") {
		t.Error("view missing [sessions] section")
	}
	if !strings.Contains(view, "> 🟢 [0] main") {
		t.Error("view missing selected numbered session row")
	}
	if !strings.Contains(view, "  🔴 [1] review") {
		t.Error("view missing secondary numbered session row")
	}
	if !strings.Contains(view, "[nodes]") {
		t.Error("view missing [nodes] section")
	}
	if !strings.Contains(view, "boss       🟢  ready") || !strings.Contains(view, "messenger  🟢  ready") {
		t.Error("view missing name-first selected-session node rows with status labels")
	}
	if strings.Contains(view, "critic") || strings.Contains(view, "worker") {
		t.Error("view leaked nodes from an unselected session")
	}
	if strings.Contains(view, "pending") || strings.Contains(view, "input") {
		t.Error("view leaked node state labels from an unselected session")
	}
	for _, forbidden := range []string{
		"1:Events",
		"2:Routing",
		"Recent Events:",
		"Routing Edges:",
		"Legend:",
		"[space: session on/off]",
		"[l: layout]",
		"[g: guard=",
		"╭",
		"│",
		"╰",
	} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("view still contains removed default-surface artifact %q: %q", forbidden, view)
		}
	}
}

func TestTUI_View_UsesAltScreen(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	view := m.View()

	if !view.AltScreen {
		t.Fatal("View().AltScreen = false, want true so the dashboard stays isolated from tmux redraws")
	}
}

func TestTUI_View_Quitting(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.quitting = true

	view := m.View().Content

	if !strings.Contains(view, "Shutting down") {
		t.Error("quitting view missing shutdown message")
	}
}

func TestTUI_View_ShowsVersion(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)
	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	view := m.View().Content
	if !strings.Contains(view, "tmux-a2a-postman "+version.Version) {
		t.Errorf("view missing title+version: want %q in view", "tmux-a2a-postman "+version.Version)
	}
}

func TestTUI_Update_DefaultSurfaceSessionNavigationKeys(t *testing.T) {
	tests := []struct {
		name      string
		start     int
		key       tea.KeyPressMsg
		wantIndex int
	}{
		{name: "j moves down", start: 0, key: tea.KeyPressMsg{Text: "j", Code: 'j'}, wantIndex: 1},
		{name: "down arrow moves down", start: 0, key: tea.KeyPressMsg{Code: tea.KeyDown}, wantIndex: 1},
		{name: "k moves up", start: 1, key: tea.KeyPressMsg{Text: "k", Code: 'k'}, wantIndex: 0},
		{name: "up arrow moves up", start: 1, key: tea.KeyPressMsg{Code: tea.KeyUp}, wantIndex: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan DaemonEvent, 10)
			defer close(ch)
			commands := make(chan TUICommand, 10)
			m := InitialModel(ch, commands, config.DefaultConfig(), "")
			m.sessions = []SessionInfo{
				{Name: "main", Enabled: true},
				{Name: "review", Enabled: false},
				{Name: "idle", Enabled: false},
			}
			m.selectedSession = tt.start

			newModel, cmd := m.Update(tt.key)
			if cmd != nil {
				t.Fatalf("Update(%q) returned cmd %v, want nil", tt.key.String(), cmd)
			}
			m = newModel.(Model)

			if m.selectedSession != tt.wantIndex {
				t.Fatalf("selectedSession after %q = %d, want %d", tt.key.String(), m.selectedSession, tt.wantIndex)
			}
			if len(commands) != 0 {
				t.Fatalf("navigation key %q emitted %d command(s), want 0", tt.key.String(), len(commands))
			}
		})
	}
}

func TestTUI_View_DefaultSurfaceNavigationCanSelectVisibleDisabledSession(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.sessions = []SessionInfo{
		{Name: "main", Enabled: true},
		{Name: "review", Enabled: false},
	}
	m.sessionSnapshots["main"] = status.SessionStatus{
		SessionName:  "main",
		VisibleState: "ready",
	}
	m.sessionSnapshots["review"] = status.SessionStatus{
		SessionName:  "review",
		VisibleState: "ready",
	}
	m.selectedSession = 0

	newModel, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("Update(down) returned cmd %v, want nil", cmd)
	}
	m = newModel.(Model)

	view := m.View().Content

	if m.selectedSession != 1 {
		t.Fatalf("selectedSession = %d, want 1", m.selectedSession)
	}
	if !strings.Contains(view, "> 🟢 [1] review") {
		t.Fatalf("view missing selected session row with canonical indicator: %q", view)
	}
}

func TestTUI_Update_DefaultSurfacePingDispatchesCommand(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)
	commands := make(chan TUICommand, 1)

	m := InitialModel(ch, commands, config.DefaultConfig(), "")
	m.sessions = []SessionInfo{
		{Name: "main", Enabled: false},
	}
	m.selectedSession = 0

	newModel, cmd := m.Update(tea.KeyPressMsg{Text: "p", Code: 'p'})
	if cmd != nil {
		t.Fatalf("Update(p) returned cmd %v, want nil", cmd)
	}
	m = newModel.(Model)

	if got := m.sessionStatus["main"]; got != "Sending ping..." {
		t.Fatalf("sessionStatus[main] = %q, want %q", got, "Sending ping...")
	}

	select {
	case sent := <-commands:
		if sent.Type != "send_ping" {
			t.Fatalf("sent.Type = %q, want %q", sent.Type, "send_ping")
		}
		if sent.Target != "main" {
			t.Fatalf("sent.Target = %q, want %q", sent.Target, "main")
		}
	default:
		t.Fatal("expected send_ping command after pressing p")
	}
}

func TestTUI_Update_DefaultSurfacePingRequiresDaemonForStatus(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.sessions = []SessionInfo{
		{Name: "main", Enabled: true},
	}
	m.selectedSession = 0

	newModel, cmd := m.Update(tea.KeyPressMsg{Text: "p", Code: 'p'})
	if cmd != nil {
		t.Fatalf("Update(p) returned cmd %v, want nil", cmd)
	}
	m = newModel.(Model)

	if got := m.sessionStatus["main"]; got != "Ping: daemon unavailable" {
		t.Fatalf("sessionStatus[main] = %q, want %q", got, "Ping: daemon unavailable")
	}
}

func TestTUI_Update_DefaultSurfaceStillIgnoresRemovedKeys(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)
	commands := make(chan TUICommand, 10)
	m := InitialModel(ch, commands, config.DefaultConfig(), "")
	m.sessions = []SessionInfo{
		{Name: "main", Enabled: true},
		{Name: "review", Enabled: true},
	}
	m.selectedSession = 0

	for _, key := range []tea.KeyPressMsg{
		{Text: "tab"},
		{Text: "1", Code: '1'},
		{Text: "2", Code: '2'},
		{Text: "g", Code: 'g'},
		{Text: "l", Code: 'l'},
		{Text: " ", Code: ' '},
	} {
		newModel, cmd := m.Update(key)
		if cmd != nil {
			t.Fatalf("Update(%q) returned cmd %v, want nil", key.Text, cmd)
		}
		m = newModel.(Model)
	}

	if m.selectedSession != 0 {
		t.Fatalf("selectedSession = %d, want unchanged 0", m.selectedSession)
	}
	if len(commands) != 0 {
		t.Fatalf("removed keys emitted %d command(s), want 0", len(commands))
	}
}

func TestTUI_MessageTruncation(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	// Add 15 messages (should keep only last 10)
	for i := 1; i <= 15; i++ {
		event := DaemonEventMsg{
			Type:    "message_received",
			Message: "Message " + string(rune('0'+i)),
		}
		newModel, _ := m.Update(event)
		m = newModel.(Model)
	}

	if len(m.events) != 10 {
		t.Errorf("event truncation: got %d events, want 10", len(m.events))
	}
}

func TestInitialModel_OwnContextID(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	cfg := config.DefaultConfig()
	m := InitialModel(ch, nil, cfg, "session-abc")
	if m.ownContextID != "session-abc" {
		t.Errorf("ownContextID = %q, want %q", m.ownContextID, "session-abc")
	}
}

func TestTUI_Update_PaneRestartRecordsRecoveryEvent(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	event := DaemonEventMsg{
		Type:    "pane_restart",
		Message: "Pane restart detected: review:critic (old: %11, new: %12)",
		Details: map[string]interface{}{
			"node":        "review:critic",
			"old_pane_id": "%11",
			"new_pane_id": "%12",
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if got := m.sessionStatus["review"]; got != event.Message {
		t.Fatalf("sessionStatus[review] = %q, want %q", got, event.Message)
	}
	if len(m.events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(m.events))
	}
	if got := m.events[0].Message; got != event.Message {
		t.Fatalf("events[0].Message = %q, want %q", got, event.Message)
	}
	if got := m.events[0].SessionName; got != "review" {
		t.Fatalf("events[0].SessionName = %q, want %q", got, "review")
	}
	if got := m.events[0].Severity; got != SeverityWarning {
		t.Fatalf("events[0].Severity = %q, want %q", got, SeverityWarning)
	}
}

func TestTUI_Update_SessionCollapsedRecordsCriticalEvent(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	event := DaemonEventMsg{
		Type:    "session_collapsed",
		Message: "Session collapsed: review (2 panes disappeared)",
		Details: map[string]interface{}{
			"session": "review",
			"nodes":   []string{"review:critic", "review:guardian"},
			"count":   2,
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if got := m.sessionStatus["review"]; got != event.Message {
		t.Fatalf("sessionStatus[review] = %q, want %q", got, event.Message)
	}
	if len(m.events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(m.events))
	}
	if got := m.events[0].Message; got != event.Message {
		t.Fatalf("events[0].Message = %q, want %q", got, event.Message)
	}
	if got := m.events[0].SessionName; got != "review" {
		t.Fatalf("events[0].SessionName = %q, want %q", got, "review")
	}
	if got := m.events[0].Severity; got != SeverityCritical {
		t.Fatalf("events[0].Severity = %q, want %q", got, SeverityCritical)
	}
}

func TestTUI_Update_PaneCollisionRecordsWarningForSession(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	event := DaemonEventMsg{
		Type:    "pane_collision",
		Message: "[COLLISION] review:critic: %11 displaced by %12",
		Details: map[string]interface{}{
			"node": "review:critic",
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if got := m.sessionStatus["review"]; got != event.Message {
		t.Fatalf("sessionStatus[review] = %q, want %q", got, event.Message)
	}
	if len(m.events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(m.events))
	}
	if got := m.events[0].Message; got != event.Message {
		t.Fatalf("events[0].Message = %q, want %q", got, event.Message)
	}
	if got := m.events[0].SessionName; got != "review" {
		t.Fatalf("events[0].SessionName = %q, want %q", got, "review")
	}
	if got := m.events[0].Severity; got != SeverityWarning {
		t.Fatalf("events[0].Severity = %q, want %q", got, SeverityWarning)
	}
}

func TestTUI_Update_PaneDisappearedRecordsDroppedStatusForSession(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	event := DaemonEventMsg{
		Type:    "pane_disappeared",
		Message: "Pane disappeared: %11 (node: review:critic)",
		Details: map[string]interface{}{
			"pane_id": "%11",
			"node":    "review:critic",
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if got := m.sessionStatus["review"]; got != event.Message {
		t.Fatalf("sessionStatus[review] = %q, want %q", got, event.Message)
	}
	if got := m.nodeStates["review:critic"]; got != "stale" {
		t.Fatalf("nodeStates[review:critic] = %q, want %q", got, "stale")
	}
	if len(m.events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(m.events))
	}
	if got := m.events[0].Message; got != event.Message {
		t.Fatalf("events[0].Message = %q, want %q", got, event.Message)
	}
	if got := m.events[0].SessionName; got != "review" {
		t.Fatalf("events[0].SessionName = %q, want %q", got, "review")
	}
	if got := m.events[0].Severity; got != SeverityDropped {
		t.Fatalf("events[0].Severity = %q, want %q", got, SeverityDropped)
	}
}
