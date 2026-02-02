package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// DaemonEvent represents an event from the daemon goroutine.
type DaemonEvent struct {
	Type    string // "message_received", "status_update", "error"
	Message string
	Details map[string]interface{}
}

// DaemonEventMsg wraps DaemonEvent for tea.Msg interface.
type DaemonEventMsg DaemonEvent

// Model holds the TUI state.
type Model struct {
	daemonEvents <-chan DaemonEvent
	messages     []string
	status       string
	nodeCount    int
	lastEvent    string
	quitting     bool
}

// InitialModel creates the initial TUI model.
func InitialModel(daemonEvents <-chan DaemonEvent) Model {
	return Model{
		daemonEvents: daemonEvents,
		messages:     []string{},
		status:       "Starting...",
		nodeCount:    0,
		lastEvent:    "",
		quitting:     false,
	}
}

// Init initializes the TUI and subscribes to daemon events.
func (m Model) Init() tea.Cmd {
	return waitForDaemonEvent(m.daemonEvents)
}

// waitForDaemonEvent waits for the next daemon event from the channel.
func waitForDaemonEvent(ch <-chan DaemonEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			// Channel closed
			return DaemonEventMsg{Type: "channel_closed"}
		}
		return DaemonEventMsg(event)
	}
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}

	case DaemonEventMsg:
		// Handle daemon event
		switch msg.Type {
		case "message_received":
			m.lastEvent = msg.Message
			m.messages = append(m.messages, msg.Message)
			// Keep only last 10 messages
			if len(m.messages) > 10 {
				m.messages = m.messages[len(m.messages)-10:]
			}
		case "status_update":
			m.status = msg.Message
			if count, ok := msg.Details["node_count"].(int); ok {
				m.nodeCount = count
			}
		case "error":
			m.messages = append(m.messages, fmt.Sprintf("ERROR: %s", msg.Message))
			if len(m.messages) > 10 {
				m.messages = m.messages[len(m.messages)-10:]
			}
		case "channel_closed":
			m.quitting = true
			return m, tea.Quit
		}
		// Wait for next event
		return m, waitForDaemonEvent(m.daemonEvents)
	}

	return m, nil
}

// View renders the TUI.
func (m Model) View() string {
	if m.quitting {
		return "Shutting down...\n"
	}

	var b strings.Builder

	// Header
	b.WriteString("=== Postman Daemon ===\n")
	b.WriteString(fmt.Sprintf("Status: %s | Nodes: %d\n", m.status, m.nodeCount))
	b.WriteString("\n")

	// Messages area
	b.WriteString("Recent Events:\n")
	if len(m.messages) == 0 {
		b.WriteString("  (no events yet)\n")
	} else {
		for _, msg := range m.messages {
			b.WriteString(fmt.Sprintf("  - %s\n", msg))
		}
	}
	b.WriteString("\n")

	// Status bar
	b.WriteString("Press 'q' or 'ctrl+c' to quit\n")

	return b.String()
}
