package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

func TestTUI_InitialModel(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch)

	if m.status != "Starting..." {
		t.Errorf("initial status: got %q, want %q", m.status, "Starting...")
	}
	if m.nodeCount != 0 {
		t.Errorf("initial nodeCount: got %d, want 0", m.nodeCount)
	}
	if len(m.messages) != 0 {
		t.Errorf("initial messages length: got %d, want 0", len(m.messages))
	}
	if m.quitting {
		t.Error("initial quitting: got true, want false")
	}
}

func TestTUI_Update_Quit(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch)

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

	m := InitialModel(ch)

	// Send message received event
	event := DaemonEventMsg{
		Type:    "message_received",
		Message: "Test message delivered",
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if len(m.messages) != 1 {
		t.Errorf("messages length: got %d, want 1", len(m.messages))
	}
	if m.messages[0] != "Test message delivered" {
		t.Errorf("message content: got %q, want %q", m.messages[0], "Test message delivered")
	}
	if m.lastEvent != "Test message delivered" {
		t.Errorf("lastEvent: got %q, want %q", m.lastEvent, "Test message delivered")
	}
}

func TestTUI_Update_StatusUpdate(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch)

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

	m := InitialModel(ch)
	m.status = "Running"
	m.nodeCount = 3
	m.messages = []string{"Message 1", "Message 2"}

	view := m.View()

	// Verify header
	if !strings.Contains(view, "=== Postman Daemon ===") {
		t.Error("view missing header")
	}
	if !strings.Contains(view, "Status: Running") {
		t.Error("view missing status")
	}
	if !strings.Contains(view, "Nodes: 3") {
		t.Error("view missing node count")
	}

	// Verify messages
	if !strings.Contains(view, "Message 1") {
		t.Error("view missing Message 1")
	}
	if !strings.Contains(view, "Message 2") {
		t.Error("view missing Message 2")
	}

	// Verify quit instruction (updated for Issue #12)
	if !strings.Contains(view, "q (quit)") {
		t.Error("view missing quit instruction")
	}
}

func TestTUI_View_Quitting(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch)
	m.quitting = true

	view := m.View()

	if !strings.Contains(view, "Shutting down") {
		t.Error("quitting view missing shutdown message")
	}
}

func TestTUI_MessageTruncation(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch)

	// Add 15 messages (should keep only last 10)
	for i := 1; i <= 15; i++ {
		event := DaemonEventMsg{
			Type:    "message_received",
			Message: "Message " + string(rune('0'+i)),
		}
		newModel, _ := m.Update(event)
		m = newModel.(Model)
	}

	if len(m.messages) != 10 {
		t.Errorf("message truncation: got %d messages, want 10", len(m.messages))
	}
}

func TestTUI_MessageList_Update(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch)

	// Send inbox_update event
	msgList := []message.MessageInfo{
		{Timestamp: "20260201-120000", From: "orchestrator", To: "worker"},
		{Timestamp: "20260201-130000", From: "observer", To: "worker"},
	}
	event := DaemonEventMsg{
		Type: "inbox_update",
		Details: map[string]interface{}{
			"messages": msgList,
		},
	}

	newModel, _ := m.Update(event)
	m = newModel.(Model)

	if len(m.messageList) != 2 {
		t.Errorf("messageList length: got %d, want 2", len(m.messageList))
	}
	if m.messageList[0].From != "orchestrator" {
		t.Errorf("first message from: got %q, want %q", m.messageList[0].From, "orchestrator")
	}
}

func TestTUI_RoutingView_AddEdge(t *testing.T) {
	ch := make(chan DaemonEvent, 10)
	defer close(ch)

	m := InitialModel(ch)
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

	m := InitialModel(ch)
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

	m := InitialModel(ch)

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
