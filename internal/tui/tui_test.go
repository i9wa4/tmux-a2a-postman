package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTUI_InitialModel(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil)

	if m.status != "Starting..." {
		t.Errorf("initial status: got %q, want %q", m.status, "Starting...")
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

	m := InitialModel(ch, nil)

	// Test 'q' key
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
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

	m := InitialModel(ch, nil)

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

	m := InitialModel(ch, nil)

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

	if m.status != "Running" {
		t.Errorf("status: got %q, want %q", m.status, "Running")
	}
	if m.nodeCount != 5 {
		t.Errorf("nodeCount: got %d, want 5", m.nodeCount)
	}
}

func TestTUI_View(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil)
	m.status = "Running"
	m.nodeCount = 3
	m.events = []EventEntry{
		{Message: "Message 1"},
		{Message: "Message 2"},
	}

	view := m.View()

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

	m := InitialModel(ch, nil)
	m.quitting = true

	view := m.View()

	if !strings.Contains(view, "Shutting down") {
		t.Error("quitting view missing shutdown message")
	}
}

func TestTUI_MessageTruncation(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil)

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

	m := InitialModel(ch, nil)
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

	m := InitialModel(ch, nil)
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

	m := InitialModel(ch, nil)

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

func TestSessionHasIdleNodes(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil)
	m.sessionNodes = map[string][]string{
		"session-a": {"worker", "observer"},
		"session-b": {"tester"},
	}
	m.nodeStates = map[string]string{
		"worker":   "active",
		"observer": "holding",
		"tester":   "gray",
	}

	// session-a has "holding" node
	if !m.sessionHasIdleNodes("session-a") {
		t.Error("session-a should have idle nodes (observer is holding)")
	}

	// session-b has no holding/dropped nodes
	if m.sessionHasIdleNodes("session-b") {
		t.Error("session-b should not have idle nodes (tester is gray)")
	}

	// non-existent session
	if m.sessionHasIdleNodes("session-c") {
		t.Error("non-existent session should not have idle nodes")
	}

	// Test dropped state
	m.nodeStates["tester"] = "dropped"
	if !m.sessionHasIdleNodes("session-b") {
		t.Error("session-b should have idle nodes (tester is dropped)")
	}
}

func TestRenderLeftPane_EmojiIndicators(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch, nil)
	m.width = 80
	m.height = 24
	m.sessions = []SessionInfo{
		{Name: "(All)", Enabled: true},
		{Name: "session-a", NodeCount: 2, Enabled: true},
		{Name: "session-b", NodeCount: 1, Enabled: false},
	}
	m.sessionNodes = map[string][]string{
		"session-a": {"worker", "observer"},
		"session-b": {"tester"},
	}
	m.nodeStates = map[string]string{
		"worker":   "active",
		"observer": "holding",
		"tester":   "gray",
	}

	result := m.renderLeftPane(25, 20)

	// Verify "(All)" has no emoji prefix
	if !strings.Contains(result, "(All)") {
		t.Error("renderLeftPane missing (All) entry")
	}

	// Verify enabled session has green emoji
	if !strings.Contains(result, "\U0001F7E2") { // 🟢
		t.Error("renderLeftPane missing green circle emoji for enabled session")
	}

	// Verify disabled session has black emoji
	if !strings.Contains(result, "\u26AB") { // ⚫
		t.Error("renderLeftPane missing black circle emoji for disabled session")
	}

	// Verify mail emoji for session with idle nodes
	if !strings.Contains(result, "\U0001F4E7") { // 📧
		t.Error("renderLeftPane missing mail emoji for session with holding node")
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
