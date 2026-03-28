package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
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
	// Issue #249: startup guard must be hard-disabled at code level on first launch.
	if m.startupGuardEnabled {
		t.Error("initial startupGuardEnabled: got true, want false (must be hard-disabled at code level)")
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
	m.generalStatus = "Running"
	m.nodeCount = 3
	m.events = []EventEntry{
		{Message: "Message 1"},
		{Message: "Message 2"},
	}

	view := m.View().Content

	// Issue #45: Verify new split layout components
	// Left pane
	if !strings.Contains(view, "Sessions") {
		t.Error("view missing left pane Sessions header")
	}

	// Right pane
	if !strings.Contains(view, "1:Events") {
		t.Error("view missing Events tab")
	}
	if !strings.Contains(view, "2:Routing") {
		t.Error("view missing Routing tab")
	}
	if !strings.Contains(view, "Recent Events:") {
		t.Error("view missing Recent Events header")
	}

	// Verify messages
	if !strings.Contains(view, "Message 1") {
		t.Error("view missing Message 1")
	}
	if !strings.Contains(view, "Message 2") {
		t.Error("view missing Message 2")
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

func TestTUI_Update_LayoutToggle(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)
	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	if m.layoutMode {
		t.Error("initial layoutMode should be false")
	}
	newModel, _ := m.Update(tea.KeyPressMsg{Text: "l", Code: 'l'})
	m = newModel.(Model)
	if !m.layoutMode {
		t.Error("layoutMode should be true after 'l' key")
	}
	newModel, _ = m.Update(tea.KeyPressMsg{Text: "l", Code: 'l'})
	m = newModel.(Model)
	if m.layoutMode {
		t.Error("layoutMode should be false after second 'l' key")
	}
}

func TestTUI_View_VerticalLayout(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)
	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.layoutMode = true
	m.sessions = []SessionInfo{
		{Name: "session-a", Enabled: true},
		{Name: "session-b", Enabled: true},
	}
	view := m.View().Content
	if !strings.Contains(view, "session-a") {
		t.Error("vertical layout missing session-a")
	}
	if !strings.Contains(view, "session-b") {
		t.Error("vertical layout missing session-b")
	}
}

func TestTUI_View_VerticalLayout_SessionStatus(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)
	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.layoutMode = true
	m.sessions = []SessionInfo{
		{Name: "session-a", Enabled: true},
		{Name: "session-b", Enabled: true},
	}
	m.sessionStatus["session-a"] = "Sending ping..."
	m.sessionStatus["session-b"] = "PING: 7/7 dispatched"

	view := m.View().Content

	if !strings.Contains(view, "Sending ping...") {
		t.Error("vertical layout missing sessionStatus for session-a")
	}
	if !strings.Contains(view, "PING: 7/7 dispatched") {
		t.Error("vertical layout missing sessionStatus for session-b")
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

func TestTUI_RoutingView_AddEdge(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.currentView = ViewRouting

	// Send config_update event with edges
	edgeList := []Edge{
		{Raw: "orchestrator -- worker"},
		{Raw: "worker -- observer"},
	}
	event := DaemonEventMsg{
		Type: "config_update",
		Details: map[string]interface{}{
			"edges": edgeList,
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if len(m.edges) != 2 {
		t.Errorf("edges length: got %d, want 2", len(m.edges))
	}
	if m.edges[0].Raw != "orchestrator -- worker" {
		t.Errorf("first edge: got %q, want %q", m.edges[0].Raw, "orchestrator -- worker")
	}
}

func TestTUI_RoutingView_RemoveEdge(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.currentView = ViewRouting
	m.edges = []Edge{
		{Raw: "orchestrator -- worker"},
		{Raw: "worker -- observer"},
	}
	m.selectedEdge = 0

	// Send config_update event with one edge removed
	edgeList := []Edge{
		{Raw: "worker -- observer"},
	}
	event := DaemonEventMsg{
		Type: "config_update",
		Details: map[string]interface{}{
			"edges": edgeList,
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if len(m.edges) != 1 {
		t.Errorf("edges length after removal: got %d, want 1", len(m.edges))
	}
	if m.selectedEdge != 0 {
		t.Errorf("selectedEdge clamping: got %d, want 0", m.selectedEdge)
	}
}

func TestTUI_HotReload(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")

	// Initial edges
	edgeList1 := []Edge{
		{Raw: "A -- B"},
	}
	event1 := DaemonEventMsg{
		Type: "config_update",
		Details: map[string]interface{}{
			"edges": edgeList1,
		},
	}
	newModel, _ := m.Update(event1)
	m = newModel.(Model)

	if len(m.edges) != 1 {
		t.Errorf("initial edges length: got %d, want 1", len(m.edges))
	}

	// Hot reload with new edges
	edgeList2 := []Edge{
		{Raw: "A -- B"},
		{Raw: "B -- C"},
	}
	event2 := DaemonEventMsg{
		Type: "config_update",
		Details: map[string]interface{}{
			"edges": edgeList2,
		},
	}
	newModel, _ = m.Update(event2)
	m = newModel.(Model)

	if len(m.edges) != 2 {
		t.Errorf("hot reloaded edges length: got %d, want 2", len(m.edges))
	}
	if m.edges[1].Raw != "B -- C" {
		t.Errorf("second edge after reload: got %q, want %q", m.edges[1].Raw, "B -- C")
	}
}

func TestParseEdgeNodes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"undirected simple", "A -- B", []string{"A", "B"}},
		{"directed simple", "A --> B", []string{"A", "B"}},
		{"undirected chain", "A -- B -- C", []string{"A", "B", "C"}},
		{"directed chain", "A --> B --> C", []string{"A", "B", "C"}},
		{"whitespace", "  A  --  B  ", []string{"A", "B"}},
		{"no separator", "A B", nil},
		{"empty string", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseEdgeNodes(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("ParseEdgeNodes(%q): got %d nodes, want %d", tt.input, len(result), len(tt.expected))
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("ParseEdgeNodes(%q)[%d]: got %q, want %q", tt.input, i, result[i], tt.expected[i])
				}
			}
		})
	}
}

func TestRenderLeftPane_EmojiIndicators(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.width = 80
	m.height = 24
	m.sessions = []SessionInfo{
		{Name: "session-a", NodeCount: 2, Enabled: true},
		{Name: "session-b", NodeCount: 1, Enabled: false},
	}
	m.sessionNodes = map[string][]string{
		"session-a": {"worker", "observer"},
		"session-b": {"tester"},
	}
	// Issue #77: Use session-prefixed keys
	m.nodeStates = map[string]string{
		"session-a:worker":   "active",
		"session-a:observer": "holding",
		"session-b:tester":   "gray",
	}

	result := m.renderLeftPane(25, 20)

	// Verify enabled session has green emoji
	if !strings.Contains(result, "\U0001F7E2") { // 🟢
		t.Error("renderLeftPane missing green circle emoji for enabled session")
	}

	// Verify disabled session has black emoji
	if !strings.Contains(result, "\u26AB") { // ⚫
		t.Error("renderLeftPane missing black circle emoji for disabled session")
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncated", "hello world", 8, "hello..."},
		{"very short max", "hello", 2, "he"},
		{"unicode", "こんにちは世界", 5, "こん..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncateString(%q, %d): got %q, want %q", tt.input, tt.maxLen, result, tt.expected)
			}
		})
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

func TestTUI_SpaceKey_GuardBlocks(t *testing.T) {
	// Create a temp baseDir that simulates another daemon owning "other-ctx/sess-name"
	baseDir := t.TempDir()
	otherCtx := filepath.Join(baseDir, "other-ctx", "sess-name")
	if err := os.MkdirAll(otherCtx, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Write current PID — always alive
	pidPath := filepath.Join(otherCtx, "postman.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.BaseDir = baseDir

	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, cfg, "own-ctx")
	m.startupGuardEnabled = true
	m.sessions = []SessionInfo{{Name: "sess-name", Enabled: false, NodeCount: 1}}
	m.selectedSession = 0

	// Simulate space key press (toggle ON)
	newModel, _ := m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	got := newModel.(Model)

	// Expect: session NOT flipped to enabled
	if got.sessions[0].Enabled {
		t.Error("guard failed: session was enabled despite owning daemon in other-ctx")
	}
	// Expect: sessionStatus for sess-name contains "already active"
	if !strings.Contains(got.sessionStatus["sess-name"], "already active") {
		t.Errorf("expected sessionStatus[%q] with 'already active', got %q", "sess-name", got.sessionStatus["sess-name"])
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

func TestTUI_Update_NodeInactivityRecordsWarningForUniqueSession(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.sessionNodes = map[string][]string{
		"review": {"critic"},
		"main":   {"worker"},
	}

	event := DaemonEventMsg{
		Type:    "node_inactivity",
		Message: "Node critic inactive for 10m0s",
		Details: map[string]interface{}{
			"node":     "critic",
			"duration": "10m0s",
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
	if got := m.events[0].SessionName; got != "review" {
		t.Fatalf("events[0].SessionName = %q, want %q", got, "review")
	}
	if got := m.events[0].Severity; got != SeverityWarning {
		t.Fatalf("events[0].Severity = %q, want %q", got, SeverityWarning)
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

func TestTUI_Update_DroppedBallRecordsSessionForQualifiedNode(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.sessionNodes = map[string][]string{
		"review": {"critic"},
		"main":   {"worker"},
	}

	event := DaemonEventMsg{
		Type:    "dropped_ball",
		Message: "Dropped ball: review:critic inactive for 30m0s",
		Details: map[string]interface{}{
			"node":     "review:critic",
			"duration": "30m0s",
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if got := m.lastEvent; got != event.Message {
		t.Fatalf("lastEvent = %q, want %q", got, event.Message)
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

func TestTUI_Update_InboxUnreadSummaryRecordsWarningForUniqueSession(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.sessionNodes = map[string][]string{
		"review": {"critic"},
		"main":   {"worker"},
	}

	event := DaemonEventMsg{
		Type:    "inbox_unread_summary",
		Message: "Node critic has 3 unread messages",
		Details: map[string]interface{}{
			"node":      "critic",
			"count":     3,
			"threshold": 2,
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
	if got := m.events[0].SessionName; got != "review" {
		t.Fatalf("events[0].SessionName = %q, want %q", got, "review")
	}
	if got := m.events[0].Severity; got != SeverityWarning {
		t.Fatalf("events[0].Severity = %q, want %q", got, SeverityWarning)
	}
}

func TestTUI_Update_UnrepliedMessageRecordsWarningForUniqueSession(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil, config.DefaultConfig(), "")
	m.sessionNodes = map[string][]string{
		"review": {"critic"},
		"main":   {"worker"},
	}

	event := DaemonEventMsg{
		Type:    "unreplied_message",
		Message: "Node critic has 2 unreplied messages",
		Details: map[string]interface{}{
			"node":  "critic",
			"count": 2,
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
	if got := m.events[0].SessionName; got != "review" {
		t.Fatalf("events[0].SessionName = %q, want %q", got, "review")
	}
	if got := m.events[0].Severity; got != SeverityWarning {
		t.Fatalf("events[0].Severity = %q, want %q", got, SeverityWarning)
	}
}
