package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/i9wa4/tmux-a2a-postman/internal/concierge"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

// ViewType represents the current TUI view.
type ViewType int

const (
	ViewEvents ViewType = iota
	ViewMessages
	ViewRouting
)

// Edge represents a routing edge definition.
type Edge struct {
	Raw string // Raw edge string (e.g., "A -- B -- C")
}

// DaemonEvent represents an event from the daemon goroutine.
type DaemonEvent struct {
	Type    string // "message_received", "status_update", "error", "inbox_update", "config_update", "concierge_status_update"
	Message string
	Details map[string]interface{}
}

// DaemonEventMsg wraps DaemonEvent for tea.Msg interface.
type DaemonEventMsg DaemonEvent

// Model holds the TUI state.
type Model struct {
	// View state
	currentView ViewType

	// Message list view
	messageList []message.MessageInfo
	selectedMsg int

	// Routing view
	edges        []Edge
	selectedEdge int

	// Concierge status
	conciergeStatus *concierge.PaneInfo

	// Shared state
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
		currentView:     ViewEvents,
		messageList:     []message.MessageInfo{},
		selectedMsg:     0,
		edges:           []Edge{},
		selectedEdge:    0,
		conciergeStatus: nil,
		daemonEvents:    daemonEvents,
		messages:        []string{},
		status:          "Starting...",
		nodeCount:       0,
		lastEvent:       "",
		quitting:        false,
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
		// Global keys
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "tab":
			// Cycle through views
			m.currentView = (m.currentView + 1) % 3
			return m, nil
		case "1":
			m.currentView = ViewEvents
			return m, nil
		case "2":
			m.currentView = ViewMessages
			return m, nil
		case "3":
			m.currentView = ViewRouting
			return m, nil
		}

		// View-specific keys
		switch m.currentView {
		case ViewMessages:
			switch msg.String() {
			case "j", "down":
				if m.selectedMsg < len(m.messageList)-1 {
					m.selectedMsg++
				}
			case "k", "up":
				if m.selectedMsg > 0 {
					m.selectedMsg--
				}
			}
		case ViewRouting:
			switch msg.String() {
			case "j", "down":
				if m.selectedEdge < len(m.edges)-1 {
					m.selectedEdge++
				}
			case "k", "up":
				if m.selectedEdge > 0 {
					m.selectedEdge--
				}
			}
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
		case "inbox_update":
			// Update message list from Details
			if msgList, ok := msg.Details["messages"].([]message.MessageInfo); ok {
				m.messageList = msgList
				// Clamp selection
				if m.selectedMsg >= len(m.messageList) {
					m.selectedMsg = len(m.messageList) - 1
				}
				if m.selectedMsg < 0 {
					m.selectedMsg = 0
				}
			}
		case "config_update":
			// Update edges from Details
			if edgeList, ok := msg.Details["edges"].([]Edge); ok {
				m.edges = edgeList
				// Clamp selection
				if m.selectedEdge >= len(m.edges) {
					m.selectedEdge = len(m.edges) - 1
				}
				if m.selectedEdge < 0 {
					m.selectedEdge = 0
				}
			}
		case "concierge_status_update":
			// Update concierge status from Details
			if paneInfo, ok := msg.Details["pane_info"].(*concierge.PaneInfo); ok {
				m.conciergeStatus = paneInfo
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

	// Concierge Status Panel
	b.WriteString(m.renderConciergePanel())
	b.WriteString("\n")

	// View tabs
	b.WriteString("[")
	if m.currentView == ViewEvents {
		b.WriteString("1:Events*")
	} else {
		b.WriteString("1:Events")
	}
	b.WriteString(" | ")
	if m.currentView == ViewMessages {
		b.WriteString("2:Messages*")
	} else {
		b.WriteString("2:Messages")
	}
	b.WriteString(" | ")
	if m.currentView == ViewRouting {
		b.WriteString("3:Routing*")
	} else {
		b.WriteString("3:Routing")
	}
	b.WriteString("]\n\n")

	// View-specific content
	switch m.currentView {
	case ViewEvents:
		b.WriteString(m.renderEventsView())
	case ViewMessages:
		b.WriteString(m.renderMessagesView())
	case ViewRouting:
		b.WriteString(m.renderRoutingView())
	}

	b.WriteString("\n")

	// Status bar
	b.WriteString("Keys: tab/1-3 (switch view) | j/k (nav) | q (quit)\n")

	return b.String()
}

func (m Model) renderEventsView() string {
	var b strings.Builder
	b.WriteString("Recent Events:\n")
	if len(m.messages) == 0 {
		b.WriteString("  (no events yet)\n")
	} else {
		for _, msg := range m.messages {
			b.WriteString(fmt.Sprintf("  - %s\n", msg))
		}
	}
	return b.String()
}

func (m Model) renderMessagesView() string {
	var b strings.Builder
	b.WriteString("Inbox Messages:\n")
	if len(m.messageList) == 0 {
		b.WriteString("  (no messages)\n")
	} else {
		for i, msg := range m.messageList {
			if i == m.selectedMsg {
				b.WriteString(fmt.Sprintf("  > %s | from: %s | to: %s\n", msg.Timestamp, msg.From, msg.To))
			} else {
				b.WriteString(fmt.Sprintf("    %s | from: %s | to: %s\n", msg.Timestamp, msg.From, msg.To))
			}
		}
	}
	return b.String()
}

func (m Model) renderRoutingView() string {
	var b strings.Builder
	b.WriteString("Routing Edges:\n")
	if len(m.edges) == 0 {
		b.WriteString("  (no edges defined)\n")
	} else {
		for i, edge := range m.edges {
			if i == m.selectedEdge {
				b.WriteString(fmt.Sprintf("  > %s\n", edge.Raw))
			} else {
				b.WriteString(fmt.Sprintf("    %s\n", edge.Raw))
			}
		}
	}
	return b.String()
}

func (m Model) renderConciergePanel() string {
	var b strings.Builder
	b.WriteString("[Concierge Status] ")

	if m.conciergeStatus == nil {
		b.WriteString("UNKNOWN (no data)")
		return b.String()
	}

	// Status indicator with color emoji
	statusEmoji := ""
	switch m.conciergeStatus.Status {
	case concierge.StatusVisible:
		statusEmoji = "🟢"
	case concierge.StatusWindowVisible:
		statusEmoji = "🟡"
	case concierge.StatusNotVisible:
		statusEmoji = "🔴"
	case concierge.StatusUnknown:
		statusEmoji = "⚪"
	case concierge.StatusInactive:
		statusEmoji = "⚫"
	}

	b.WriteString(fmt.Sprintf("%s %s", statusEmoji, m.conciergeStatus.Status))

	// Additional info
	if m.conciergeStatus.PaneID != "" {
		b.WriteString(fmt.Sprintf(" | Pane: %s", m.conciergeStatus.PaneID))
	}

	return b.String()
}
